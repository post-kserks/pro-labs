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
	"time"
)

const (
	OpInsert         byte = 0x01
	OpUpdate         byte = 0x02
	OpDelete         byte = 0x03
	OpCreateDatabase byte = 0x10
	OpDropDatabase   byte = 0x11
	OpCreateTable    byte = 0x12
	OpDropTable      byte = 0x13
	OpVacuum         byte = 0x14
	OpAlterTable     byte = 0x15
	OpCheckpoint     byte = 0xF0

	// Page engine operations
	OpPageInsert     byte = 0x20 // вставка tuple на страницу
	OpPageDelete     byte = 0x21 // пометка tuple как dead (XMax)
	OpPageUpdateXMax byte = 0x22 // обновление XMax (при DELETE/UPDATE)
	OpPageNewPage    byte = 0x23 // выделение новой страницы
	OpVacuumBegin    byte = 0x30
	OpVacuumCommit   byte = 0x31
	OpAbort          byte = 0x40
	OpCommit         byte = 0x50

	// Schema rewrite operations
	OpSchemaWrite   byte = 0x60 // schema file write
	OpRewriteBegin  byte = 0x61 // start table rewrite (ALTER TABLE ADD/DROP COLUMN)
	OpRewriteData   byte = 0x62 // rewrite data chunk
	OpRewriteCommit byte = 0x63 // rewrite complete

	// Full page image for torn page protection
	OpFullPageImage byte = 0x70 // полный образ страницы перед модификацией
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

// WALPageInsertPayload — payload для OpPageInsert
type WALPageInsertPayload struct {
	DB        string
	Table     string
	SegmentNo uint16
	PageNo    uint32
	SlotNo    uint16
	XID       uint64 // транзакция, создавшая tuple
	TupleData []byte // полные данные tuple (header + attrs)
}

// WALPageDeletePayload — payload для OpPageDelete/OpPageUpdateXMax
type WALPageDeletePayload struct {
	DB        string
	Table     string
	SegmentNo uint16
	PageNo    uint32
	SlotNo    uint16
	XMax      uint64 // XID транзакции удаляющей tuple
}

// WALVacuumPayload — payload для OpVacuumBegin/OpVacuumCommit
type WALVacuumPayload struct {
	DB         string
	Table      string
	ShadowPath string
}

// WALSchemaWritePayload — payload для OpSchemaWrite
type WALSchemaWritePayload struct {
	DB     string
	Table  string
	Schema string // JSON schema
}

// WALRewritePayload — payload для OpRewriteBegin/OpRewriteCommit
type WALRewritePayload struct {
	DB    string
	Table string
}

// CheckpointPayload — payload для OpCheckpoint
type CheckpointPayload struct {
	LSN uint64
}

// FullPageImagePayload — payload для OpFullPageImage (защита от torn pages)
type FullPageImagePayload struct {
	DB        string
	Table     string
	SegmentNo uint16
	PageNo    uint32
	PageData  []byte // полный образ страницы (8KB)
}

type WAL struct {
	file          *os.File
	mu            sync.Mutex
	nextTxID      atomic.Uint64
	path          string
	syncCounter   int
	SyncBatchSize int // number of writes between fsyncs (0 = sync every write)
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
		file:          file,
		path:          path,
		SyncBatchSize: 1,
	}

	maxTx, err := w.scanAndTruncate()
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

	payload, err := json.Marshal(CheckpointPayload{LSN: w.nextTxID.Load()})
	if err != nil {
		return fmt.Errorf("wal: marshal checkpoint payload: %w", err)
	}
	record, err := buildRecord(w.nextTxID.Add(1), OpCheckpoint, payload)
	if err != nil {
		return err
	}
	if _, err := w.file.Write(record); err != nil {
		return fmt.Errorf("wal: write checkpoint: %w", err)
	}
	if err := w.file.Sync(); err != nil {
		return fmt.Errorf("wal: sync checkpoint: %w", err)
	}
	if err := w.file.Truncate(0); err != nil {
		return fmt.Errorf("wal: truncate after checkpoint: %w", err)
	}
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("wal: seek after truncate: %w", err)
	}
	return nil
}

func (w *WAL) Recover() ([]Entry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("wal: seek start: %w", err)
	}

	var entries []Entry
	var maxTxID uint64
	for {
		entry, _, err := w.readEntryFrom(w.file)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			break
		}
		if entry.TxID > maxTxID {
			maxTxID = entry.TxID
		}
		entries = append(entries, entry)
	}

	if maxTxID > w.nextTxID.Load() {
		w.nextTxID.Store(maxTxID)
	}

	if _, err := w.file.Seek(0, io.SeekEnd); err != nil {
		return nil, fmt.Errorf("wal: seek end: %w", err)
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

func (w *WAL) CurrentTxID() uint64 {
	return w.nextTxID.Load()
}

// AppendWithTx записывает запись в WAL с указанным txID (не инкрементирует автоматически).
func (w *WAL) AppendWithTx(txID uint64, opType byte, payload interface{}) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("wal: marshal payload: %w", err)
	}
	return w.appendBytesLockedWithTx(txID, opType, payloadBytes)
}

// WriteFullPageImage записывает полный образ страницы в WAL для защиты от torn pages.
// Вызывается ПЕРЕД модификацией страницы на диске.
func (w *WAL) WriteFullPageImage(txID uint64, db, table string, segmentNo uint16, pageNo uint32, pageData []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	payload := FullPageImagePayload{
		DB:        db,
		Table:     table,
		SegmentNo: segmentNo,
		PageNo:    pageNo,
		PageData:  pageData,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("wal: marshal full page image: %w", err)
	}
	_, err = w.appendBytesLockedWithTx(txID, OpFullPageImage, payloadBytes)
	return err
}

func (w *WAL) appendBytesLockedWithTx(txID uint64, opType byte, payload []byte) (uint64, error) {
	record, err := buildRecord(txID, opType, payload)
	if err != nil {
		return 0, err
	}

	if _, err := w.file.Write(record); err != nil {
		return 0, fmt.Errorf("wal: write: %w", err)
	}

	// Batching fsyncs: same tradeoff as appendBytesLocked.
	if w.SyncBatchSize > 0 {
		w.syncCounter++
		if w.syncCounter >= w.SyncBatchSize {
			w.syncCounter = 0
			if err := w.file.Sync(); err != nil {
				return 0, fmt.Errorf("wal: sync: %w", err)
			}
		}
	} else {
		if err := w.file.Sync(); err != nil {
			return 0, fmt.Errorf("wal: sync: %w", err)
		}
	}

	if txID >= w.nextTxID.Load() {
		w.nextTxID.Store(txID + 1)
	}

	return txID, nil
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

	// Batching fsyncs: trading a small window of potential data loss on crash
	// (at most SyncBatchSize unwritten records) for a significant throughput
	// improvement by amortizing the cost of expensive fsync syscalls.
	if w.SyncBatchSize > 0 {
		w.syncCounter++
		if w.syncCounter >= w.SyncBatchSize {
			w.syncCounter = 0
			if err := w.file.Sync(); err != nil {
				return 0, fmt.Errorf("wal: sync: %w", err)
			}
		}
	} else {
		if err := w.file.Sync(); err != nil {
			return 0, fmt.Errorf("wal: sync: %w", err)
		}
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

	// CRC32 используется для целостности данных (обнаружение битовых сбоев,
	// неполных записей на диске), НЕ для криптографической безопасности.
	// WAL хранится на локальном диске — если атакующий имеет доступ к файловой
	// системе, он уже может модифицировать heap-файлы напрямую.
	// Для security boundary используется HMAC-SHA256 в auth/manager.go.
	crc := crc32.ChecksumIEEE(body.Bytes())
	if err := binary.Write(&body, binary.LittleEndian, crc); err != nil {
		return nil, fmt.Errorf("wal: encode checksum: %w", err)
	}

	return body.Bytes(), nil
}

// readEntryFrom reads a single WAL entry from the current file position.
// Returns the entry, the number of bytes read, and any error.
// Returns io.EOF when no more entries.
// Reuses internal buffers to minimize allocations on hot path.
func (w *WAL) readEntryFrom(f *os.File) (Entry, int64, error) {
	magic := make([]byte, 4)
	if _, err := io.ReadFull(f, magic); err != nil {
		return Entry{}, 0, err
	}
	if string(magic) != recordMagic {
		return Entry{}, 0, io.EOF
	}

	hdr := make([]byte, 13) // txID(8) + opType(1) + payloadLen(4)
	if _, err := io.ReadFull(f, hdr); err != nil {
		return Entry{}, 0, io.ErrUnexpectedEOF
	}
	txID := binary.LittleEndian.Uint64(hdr[0:8])
	opType := hdr[8]
	payloadLen := binary.LittleEndian.Uint32(hdr[9:13])

	if payloadLen > maxPayloadSize {
		return Entry{}, 0, fmt.Errorf("wal: payload too large (%d bytes)", payloadLen)
	}

	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(f, payload); err != nil {
		return Entry{}, 0, io.ErrUnexpectedEOF
	}

	crcBuf := make([]byte, 4)
	if _, err := io.ReadFull(f, crcBuf); err != nil {
		return Entry{}, 0, io.ErrUnexpectedEOF
	}
	storedCRC := binary.LittleEndian.Uint32(crcBuf)

	// Verify checksum: magic(4) + hdr(13) + payload
	checksumData := make([]byte, 0, 4+13+int(payloadLen))
	checksumData = append(checksumData, magic...)
	checksumData = append(checksumData, hdr...)
	checksumData = append(checksumData, payload...)
	calculated := crc32.ChecksumIEEE(checksumData)
	if storedCRC != calculated {
		return Entry{}, 0, fmt.Errorf("wal: checksum mismatch")
	}

	totalSize := int64(4 + 13 + int(payloadLen) + 4)
	return Entry{TxID: txID, OpType: opType, Payload: payload}, totalSize, nil
}

// scanAndTruncate streams through the WAL to find maxTxID and truncate
// any corrupt tail. Called only during Open(). Does NOT load entries into memory.
func (w *WAL) scanAndTruncate() (uint64, error) {
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return 0, fmt.Errorf("wal: seek start: %w", err)
	}

	var maxTxID uint64
	var validEnd int64

	for {
		entry, size, err := w.readEntryFrom(w.file)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			break
		}
		if entry.TxID > maxTxID {
			maxTxID = entry.TxID
		}
		validEnd += size
	}

	// Drop any corrupt or partially written tail
	if info, err := w.file.Stat(); err == nil && info.Size() > validEnd {
		if err := w.file.Truncate(validEnd); err != nil {
			corruptPath := w.path + fmt.Sprintf(".corrupt.%d", time.Now().Unix())
			w.file.Close()
			if renameErr := os.Rename(w.path, corruptPath); renameErr != nil {
				return 0, fmt.Errorf(
					"FATAL: WAL is corrupt and cannot be truncated or renamed. "+
						"Manual intervention required. Corrupt WAL: %s. Error: %w", w.path, renameErr)
			}
			newFile, openErr := os.OpenFile(w.path, os.O_CREATE|os.O_RDWR, 0o644)
			if openErr != nil {
				return 0, fmt.Errorf("failed to create new WAL after corrupt rename: %w", openErr)
			}
			w.file = newFile
			return maxTxID, nil
		}
	}

	if _, err := w.file.Seek(0, io.SeekEnd); err != nil {
		return 0, fmt.Errorf("wal: seek end: %w", err)
	}

	return maxTxID, nil
}

// AnalyzeTransactions анализирует WAL потоково, не загружая все записи в память.
// Определяет какие транзакции закоммичены, а какие остались незавершёнными.
func (w *WAL) AnalyzeTransactions() (committed map[uint64]bool, inProgress map[uint64]bool, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return nil, nil, fmt.Errorf("wal: seek start: %w", err)
	}

	committed = make(map[uint64]bool)
	inProgress = make(map[uint64]bool)

	for {
		entry, _, err := w.readEntryFrom(w.file)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			break
		}

		switch entry.OpType {
		case OpCommit:
			committed[entry.TxID] = true
			delete(inProgress, entry.TxID)
		case OpAbort:
			delete(inProgress, entry.TxID)
			delete(committed, entry.TxID)
		default:
			if entry.TxID != 0 {
				if !committed[entry.TxID] {
					inProgress[entry.TxID] = true
				}
			}
		}
	}

	if _, err := w.file.Seek(0, io.SeekEnd); err != nil {
		return nil, nil, fmt.Errorf("wal: seek end: %w", err)
	}

	return committed, inProgress, nil
}

// Replay воспроизводит все записи WAL потоково, вызывая callback для каждой операции.
func (w *WAL) Replay(callback func(Entry) error) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("wal: seek start: %w", err)
	}

	for {
		entry, _, err := w.readEntryFrom(w.file)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			break
		}
		if err := callback(entry); err != nil {
			return fmt.Errorf("wal replay: %w", err)
		}
	}

	if _, err := w.file.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("wal: seek end: %w", err)
	}

	return nil
}

// ReplayTransaction воспроизводит записи конкретной транзакции потоково.
func (w *WAL) ReplayTransaction(txID uint64, callback func(Entry) error) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("wal: seek start: %w", err)
	}

	for {
		entry, _, err := w.readEntryFrom(w.file)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			break
		}
		if entry.TxID == txID {
			if err := callback(entry); err != nil {
				return fmt.Errorf("wal replay tx %d: %w", txID, err)
			}
		}
	}

	if _, err := w.file.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("wal: seek end: %w", err)
	}

	return nil
}

// Flush синхронизирует WAL файл на диск и возвращает текущий LSN.
func (w *WAL) Flush() (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.file.Sync(); err != nil {
		return 0, fmt.Errorf("wal flush: %w", err)
	}

	return w.nextTxID.Load(), nil
}

// FindLastVacuumCommit ищет последний OpVacuumCommit для указанной таблицы потоково.
func (w *WAL) FindLastVacuumCommit(db, table string) (bool, uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return false, 0, fmt.Errorf("wal: seek start: %w", err)
	}

	var lastMatch *Entry
	for {
		entry, _, err := w.readEntryFrom(w.file)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			break
		}
		if entry.OpType == OpVacuumCommit || entry.OpType == OpVacuumBegin {
			var payload WALVacuumPayload
			if err := json.Unmarshal(entry.Payload, &payload); err != nil {
				continue
			}
			if payload.DB == db && payload.Table == table {
				e := entry
				lastMatch = &e
			}
		}
	}

	if _, err := w.file.Seek(0, io.SeekEnd); err != nil {
		return false, 0, fmt.Errorf("wal: seek end: %w", err)
	}

	if lastMatch == nil {
		return false, 0, nil
	}
	if lastMatch.OpType == OpVacuumCommit {
		return true, lastMatch.TxID, nil
	}
	return false, lastMatch.TxID, nil
}
