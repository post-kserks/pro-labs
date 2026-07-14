package wal

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"log/slog"
	"os"
	"time"
)

// Checkpoint truncates WAL after checkpoint. For correct checkpoint
// use WriteCheckpointRecord() + catalog save + TruncateWAL().
// This method truncates WAL BEFORE saving catalog — crash between truncate and
// catalog save will lose all entries.
//
// Deprecated: use doCheckpoint() in page_engine.go.
func (w *WAL) Checkpoint() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	payload, err := EncodeWALPayloadBinary(CheckpointPayload{LSN: w.nextTxID.Load()})
	if err != nil {
		return fmt.Errorf("wal: marshal checkpoint payload: %w", err)
	}
	record, err := buildRecord(w.nextTxID.Add(1), OpCheckpoint, payload, nil)
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

// WriteCheckpointRecord writes a checkpoint record to WAL and syncs,
// but does NOT truncate the file. Returns LSN (TxID) of the checkpoint record.
// Used by doCheckpoint for ordering: checkpoint record first,
// then catalog save (so recovery can determine checkpoint LSN).
func (w *WAL) WriteCheckpointRecord() (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	payload, err := EncodeWALPayloadBinary(CheckpointPayload{LSN: w.nextTxID.Load()})
	if err != nil {
		return 0, fmt.Errorf("wal: marshal checkpoint payload: %w", err)
	}
	txID := w.nextTxID.Add(1)
	record, err := buildRecord(txID, OpCheckpoint, payload, nil)
	if err != nil {
		return 0, err
	}
	if _, err := w.file.Write(record); err != nil {
		return 0, fmt.Errorf("wal: write checkpoint: %w", err)
	}
	if err := w.file.Sync(); err != nil {
		return 0, fmt.Errorf("wal: sync checkpoint: %w", err)
	}
	return txID, nil
}

// TruncateWAL truncates WAL file after checkpoint.
// Called after saving catalog so recovery can
// determine checkpoint LSN from catalog before reading WAL.
func (w *WAL) TruncateWAL() error {
	w.mu.Lock()
	defer w.mu.Unlock()

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
		if entry.Encrypted && w.em == nil {
			return nil, fmt.Errorf("wal: record at txID %d is encrypted but no EncryptionManager is set", entry.TxID)
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

// readEntryFrom reads a single WAL entry from the current file position.
// Returns the entry, the number of bytes read, and any error.
// Returns io.EOF when no more entries.
// Uses a single read for the fixed header to minimize I/O syscalls.
// Layout: magic(4) + txID(8) + opType(1) + encrypted(1) + keyVersion(4) + nonce(12) + payloadLen(4) + payload + crc(4)
func (w *WAL) readEntryFrom(f *os.File) (Entry, int64, error) {
	fixedHdr := make([]byte, 34) // magic(4) + txID(8) + opType(1) + encrypted(1) + keyVersion(4) + nonce(12) + payloadLen(4)
	if _, err := io.ReadFull(f, fixedHdr); err != nil {
		return Entry{}, 0, err
	}
	if string(fixedHdr[:4]) != recordMagic {
		return Entry{}, 0, io.EOF
	}
	txID := binary.LittleEndian.Uint64(fixedHdr[4:12])
	opType := fixedHdr[12]
	isEncrypted := fixedHdr[13] != 0
	keyVersion := binary.LittleEndian.Uint32(fixedHdr[14:18])
	var nonce [12]byte
	copy(nonce[:], fixedHdr[18:30])
	payloadLen := binary.LittleEndian.Uint32(fixedHdr[30:34])

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

	// Verify checksum incrementally: fixedHdr(34) + payload
	calculated := crc32.ChecksumIEEE(fixedHdr)
	calculated = crc32.Update(calculated, crc32.IEEETable, payload)
	if storedCRC != calculated {
		return Entry{}, 0, fmt.Errorf("wal: checksum mismatch")
	}

	// Decrypt if encrypted
	var stillEncrypted bool
	if isEncrypted {
		if w.em != nil {
			aad := make([]byte, 8)
			binary.LittleEndian.PutUint64(aad, txID)
			plaintext, err := w.em.DecryptPage(nonce[:], payload, aad, keyVersion)
			if err != nil {
				return Entry{}, 0, fmt.Errorf("wal: decrypt payload: %w", err)
			}
			payload = plaintext
		} else {
			stillEncrypted = true
		}
	}

	totalSize := int64(34 + int(payloadLen) + 4)
	return Entry{TxID: txID, OpType: opType, Payload: payload, Encrypted: stillEncrypted}, totalSize, nil
}

// scanAndTruncate streams through the WAL to find maxTxID and truncate
// any corrupt tail. Called only during Open(). Does NOT load entries into memory.
// Tries to resync after corrupt entries by searching for the next magic bytes.
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
			// Corrupt entry — try to resync by scanning for next magic bytes
			corruptOffset := validEnd
			slog.Warn("wal: corrupt entry, attempting resync",
				"offset", corruptOffset, "error", err)

			resynced := false
			for {
				// Read one byte at a time looking for 'V' (start of "VDB1")
				var b [1]byte
				if _, readErr := w.file.Read(b[:]); readErr != nil {
					break
				}
				if b[0] != recordMagic[0] {
					continue
				}
				// Found 'V' — check if full magic matches
				peek := make([]byte, 3)
				if _, readErr := io.ReadFull(w.file, peek); readErr != nil {
					break
				}
				if string(append(b[:], peek...)) == recordMagic {
					// Found valid magic — seek back 4 bytes and try reading an entry
					newPos, _ := w.file.Seek(-4, io.SeekCurrent)
					if testEntry, testSize, testErr := w.readEntryFrom(w.file); testErr == nil {
						// Valid entry found — resume from here
						if testEntry.TxID > maxTxID {
							maxTxID = testEntry.TxID
						}
						validEnd = newPos + testSize
						resynced = true
						slog.Info("wal: resynced after corrupt entry",
							"corrupt_at", corruptOffset, "resynced_at", newPos)
						break
					}
				}
			}
			if !resynced {
				slog.Warn("wal: could not resync, truncating at last valid offset",
					"offset", validEnd)
				break
			}
			continue
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

// AnalyzeTransactions analyzes WAL streaming, without loading all entries into memory.
// Determines which transactions are committed and which remain in-progress.
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

// Replay replays all WAL entries, calling a callback for each operation.
// Entries are first collected under w.mu, then callback is called without holding
// the lock — this prevents WAL↔PageEngine deadlock (Bug lock ordering).
func (w *WAL) Replay(callback func(Entry) error) error {
	w.mu.Lock()

	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		w.mu.Unlock()
		return fmt.Errorf("wal: seek start: %w", err)
	}

	var entries []Entry
	for {
		entry, _, err := w.readEntryFrom(w.file)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			w.mu.Unlock()
			return fmt.Errorf("wal: read entry: %w", err)
		}
		entries = append(entries, entry)
	}

	if _, err := w.file.Seek(0, io.SeekEnd); err != nil {
		w.mu.Unlock()
		return fmt.Errorf("wal: seek end: %w", err)
	}

	w.mu.Unlock()

	for _, entry := range entries {
		if err := callback(entry); err != nil {
			return fmt.Errorf("wal replay: %w", err)
		}
	}

	return nil
}

// ReplayTransaction replays entries of a specific transaction.
// Entries are first collected under w.mu, then callback is called without holding
// the lock — prevents WAL↔PageEngine deadlock.
func (w *WAL) ReplayTransaction(txID uint64, callback func(Entry) error) error {
	w.mu.Lock()

	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		w.mu.Unlock()
		return fmt.Errorf("wal: seek start: %w", err)
	}

	var entries []Entry
	for {
		entry, _, err := w.readEntryFrom(w.file)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			w.mu.Unlock()
			return fmt.Errorf("wal: read entry: %w", err)
		}
		if entry.TxID == txID {
			entries = append(entries, entry)
		}
	}

	if _, err := w.file.Seek(0, io.SeekEnd); err != nil {
		w.mu.Unlock()
		return fmt.Errorf("wal: seek end: %w", err)
	}

	w.mu.Unlock()

	for _, entry := range entries {
		if err := callback(entry); err != nil {
			return fmt.Errorf("wal replay tx %d: %w", txID, err)
		}
	}

	return nil
}

// FindLastVacuumCommit searches for the last OpVacuumCommit for the given table streaming.
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
			decoded, err := DecodeWALPayload(entry.Payload, entry.OpType)
			if err != nil {
				continue
			}
			payload, ok := decoded.(WALVacuumPayload)
			if !ok {
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
