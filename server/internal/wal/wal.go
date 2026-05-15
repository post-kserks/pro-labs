package wal

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

const (
	OpInsert         byte = 0x01
	OpUpdate         byte = 0x02
	OpDelete         byte = 0x03
	OpCreateDatabase byte = 0x10
	OpDropDatabase   byte = 0x11
	OpCreateTable    byte = 0x12
	OpDropTable      byte = 0x13
	OpCheckpoint     byte = 0xF0
)

const (
	recordMagic    = "VDB1"
	maxPayloadSize = 32 << 20
)

type Entry struct {
	TxID    uint64
	OpType  byte
	Payload []byte
}

type WAL struct {
	file     *os.File
	mu       sync.Mutex
	nextTxID atomic.Uint64
	path     string
}

func Open(path string) (*WAL, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create wal dir: %w", err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open wal file: %w", err)
	}

	w := &WAL{
		file: file,
		path: path,
	}

	_, maxTx, err := w.readEntriesLocked(true)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	w.nextTxID.Store(maxTx)
	return w, nil
}

func (w *WAL) Append(opType byte, payload interface{}) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("wal: marshal payload: %w", err)
	}
	return w.appendBytesLocked(opType, payloadBytes)
}

func (w *WAL) Checkpoint() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.file.Truncate(0); err != nil {
		return fmt.Errorf("wal: truncate: %w", err)
	}
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("wal: seek after truncate: %w", err)
	}
	if err := w.file.Sync(); err != nil {
		return fmt.Errorf("wal: sync after truncate: %w", err)
	}
	return nil
}

func (w *WAL) Recover() ([]Entry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	entries, maxTx, err := w.readEntriesLocked(true)
	if err != nil {
		return nil, err
	}
	if maxTx > w.nextTxID.Load() {
		w.nextTxID.Store(maxTx)
	}
	return entries, nil
}

func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return nil
	}
	if err := w.file.Sync(); err != nil {
		return err
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *WAL) appendBytesLocked(opType byte, payload []byte) (uint64, error) {
	txID := w.nextTxID.Add(1)

	record, err := buildRecord(txID, opType, payload)
	if err != nil {
		return 0, err
	}

	if _, err := w.file.Write(record); err != nil {
		return 0, fmt.Errorf("wal: write: %w", err)
	}
	if err := w.file.Sync(); err != nil {
		return 0, fmt.Errorf("wal: sync: %w", err)
	}

	return txID, nil
}

func buildRecord(txID uint64, opType byte, payload []byte) ([]byte, error) {
	if len(payload) > maxPayloadSize {
		return nil, fmt.Errorf("wal: payload too large (%d bytes)", len(payload))
	}

	var body bytes.Buffer
	body.WriteString(recordMagic)
	if err := binary.Write(&body, binary.LittleEndian, txID); err != nil {
		return nil, fmt.Errorf("wal: encode tx id: %w", err)
	}
	body.WriteByte(opType)
	if err := binary.Write(&body, binary.LittleEndian, uint32(len(payload))); err != nil {
		return nil, fmt.Errorf("wal: encode payload length: %w", err)
	}
	body.Write(payload)

	crc := crc32.ChecksumIEEE(body.Bytes())
	if err := binary.Write(&body, binary.LittleEndian, crc); err != nil {
		return nil, fmt.Errorf("wal: encode checksum: %w", err)
	}

	return body.Bytes(), nil
}

func (w *WAL) readEntriesLocked(resetOnCheckpoint bool) ([]Entry, uint64, error) {
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return nil, 0, fmt.Errorf("wal: seek start: %w", err)
	}

	entries := make([]Entry, 0, 16)
	var maxTxID uint64

	for {
		magic := make([]byte, 4)
		if _, err := io.ReadFull(w.file, magic); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return nil, 0, fmt.Errorf("wal: read magic: %w", err)
		}
		if string(magic) != recordMagic {
			break
		}

		checksumBuf := bytes.NewBuffer(make([]byte, 0, 64))
		checksumBuf.Write(magic)

		txIDBytes := make([]byte, 8)
		if _, err := io.ReadFull(w.file, txIDBytes); err != nil {
			break
		}
		checksumBuf.Write(txIDBytes)
		txID := binary.LittleEndian.Uint64(txIDBytes)
		if txID > maxTxID {
			maxTxID = txID
		}

		opBytes := make([]byte, 1)
		if _, err := io.ReadFull(w.file, opBytes); err != nil {
			break
		}
		checksumBuf.Write(opBytes)
		opType := opBytes[0]

		lengthBytes := make([]byte, 4)
		if _, err := io.ReadFull(w.file, lengthBytes); err != nil {
			break
		}
		checksumBuf.Write(lengthBytes)
		payloadLen := binary.LittleEndian.Uint32(lengthBytes)
		if payloadLen > maxPayloadSize {
			break
		}

		payload := make([]byte, payloadLen)
		if _, err := io.ReadFull(w.file, payload); err != nil {
			break
		}
		checksumBuf.Write(payload)

		crcBytes := make([]byte, 4)
		if _, err := io.ReadFull(w.file, crcBytes); err != nil {
			break
		}
		storedCRC := binary.LittleEndian.Uint32(crcBytes)
		calculated := crc32.ChecksumIEEE(checksumBuf.Bytes())
		if storedCRC != calculated {
			break
		}

		if opType == OpCheckpoint && resetOnCheckpoint {
			entries = entries[:0]
			continue
		}

		entries = append(entries, Entry{
			TxID:    txID,
			OpType:  opType,
			Payload: payload,
		})
	}

	if _, err := w.file.Seek(0, io.SeekEnd); err != nil {
		return nil, 0, fmt.Errorf("wal: seek end: %w", err)
	}

	return entries, maxTxID, nil
}
