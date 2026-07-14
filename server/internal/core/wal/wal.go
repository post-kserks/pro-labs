package wal

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"vaultdb/internal/core/crypto"
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
	OpPageInsert     byte = 0x20 // insert tuple into page
	OpPageDelete     byte = 0x21 // mark tuple as dead (XMax)
	OpPageUpdateXMax byte = 0x22 // update XMax (on DELETE/UPDATE)
	OpPageNewPage    byte = 0x23 // allocate new page
	OpVacuumBegin    byte = 0x30
	OpVacuumCommit   byte = 0x31
	OpAbort          byte = 0x40
	OpCommit         byte = 0x50

	// Schema rewrite operations
	OpSchemaWrite   byte = 0x60 // schema file write
	OpRewriteBegin  byte = 0x61 // start table rewrite (ALTER TABLE ADD/DROP COLUMN)
	OpRewriteData   byte = 0x62 // rewrite data chunk
	OpRewriteCommit byte = 0x63 // rewrite complete

	// Truncate table operation (bulk delete without per-row WAL)
	OpTruncateTable byte = 0x64

	// Full page image for torn page protection
	OpFullPageImage byte = 0x70 // full page image before modification
)

const (
	recordMagic    = "VDB1"
	maxPayloadSize = 32 << 20
)

type Entry struct {
	TxID      uint64
	OpType    byte
	Payload   []byte
	Encrypted bool // true if payload is still ciphertext (EM was nil during read)
}

// WALPageInsertPayload — payload for OpPageInsert
type WALPageInsertPayload struct {
	DB        string
	Table     string
	SegmentNo uint16
	PageNo    uint32
	SlotNo    uint16
	XID       uint64 // transaction that created the tuple
	TupleData []byte // full tuple data (header + attrs)
}

// WALPageDeletePayload — payload for OpPageDelete/OpPageUpdateXMax
type WALPageDeletePayload struct {
	DB        string
	Table     string
	SegmentNo uint16
	PageNo    uint32
	SlotNo    uint16
	XMax      uint64 // XID of the transaction deleting the tuple
}

// WALVacuumPayload — payload for OpVacuumBegin/OpVacuumCommit
type WALVacuumPayload struct {
	DB         string
	Table      string
	ShadowPath string
}

// WALSchemaWritePayload — payload for OpSchemaWrite
type WALSchemaWritePayload struct {
	DB     string
	Table  string
	Schema string // JSON schema
}

// WALRewritePayload — payload for OpRewriteBegin/OpRewriteCommit
type WALRewritePayload struct {
	DB    string
	Table string
}

// WALTruncateTablePayload — payload for OpTruncateTable
type WALTruncateTablePayload struct {
	DB    string
	Table string
}

// CheckpointPayload — payload for OpCheckpoint
type CheckpointPayload struct {
	LSN uint64
}

// FullPageImagePayload — payload for OpFullPageImage (torn page protection)
type FullPageImagePayload struct {
	DB        string
	Table     string
	SegmentNo uint16
	PageNo    uint32
	PageData  []byte // full page image (8KB)
}

// WALRecord is a pre-built WAL record ready for batched writing.
type WALRecord struct {
	Data []byte // fully serialized record (magic + txID + opType + ... + crc)
	TxID uint64
}

// ---------------------------------------------------------------------------
// Binary payload encoding
// ---------------------------------------------------------------------------

// Binary payload marker byte. All binary-encoded WAL payloads start with this
// byte so we can distinguish them from legacy JSON payloads (which start with '{').
const binaryPayloadMarker byte = 0x01

// EncodeWALPayloadBinary encodes a typed WAL payload to compact binary format.
// Falls back to JSON for unknown types.
func EncodeWALPayloadBinary(payload interface{}) ([]byte, error) {
	switch p := payload.(type) {
	case WALPageInsertPayload:
		return encodePageInsertBinary(p)
	case WALPageDeletePayload:
		return encodePageDeleteBinary(p)
	case WALSchemaWritePayload:
		return encodeSchemaWriteBinary(p)
	case WALVacuumPayload:
		return encodeVacuumBinary(p)
	case WALRewritePayload:
		return encodeRewriteBinary(p)
	case WALTruncateTablePayload:
		return encodeTruncateTableBinary(p)
	case CheckpointPayload:
		return encodeCheckpointBinary(p)
	case FullPageImagePayload:
		return encodeFullPageImageBinary(p)
	default:
		return json.Marshal(payload)
	}
}

// DecodeWALPayload decodes a WAL payload from bytes, auto-detecting binary vs JSON.
func DecodeWALPayload(data []byte, opType byte) (interface{}, error) {
	if len(data) == 0 {
		return nil, nil
	}
	if data[0] == binaryPayloadMarker {
		return decodeBinaryPayload(data[1:], opType)
	}
	return decodeLegacyJSONPayload(data, opType)
}

func decodeLegacyJSONPayload(data []byte, opType byte) (interface{}, error) {
	switch opType {
	case OpPageInsert:
		var p WALPageInsertPayload
		if err := json.Unmarshal(data, &p); err != nil {
			return nil, err
		}
		return p, nil
	case OpPageDelete, OpPageUpdateXMax:
		var p WALPageDeletePayload
		if err := json.Unmarshal(data, &p); err != nil {
			return nil, err
		}
		return p, nil
	case OpSchemaWrite:
		var p WALSchemaWritePayload
		if err := json.Unmarshal(data, &p); err != nil {
			return nil, err
		}
		return p, nil
	case OpVacuumBegin, OpVacuumCommit:
		var p WALVacuumPayload
		if err := json.Unmarshal(data, &p); err != nil {
			return nil, err
		}
		return p, nil
	case OpRewriteBegin, OpRewriteCommit, OpRewriteData:
		var p WALRewritePayload
		if err := json.Unmarshal(data, &p); err != nil {
			return nil, err
		}
		return p, nil
	case OpTruncateTable:
		var p WALTruncateTablePayload
		if err := json.Unmarshal(data, &p); err != nil {
			return nil, err
		}
		return p, nil
	case OpCheckpoint:
		var p CheckpointPayload
		if err := json.Unmarshal(data, &p); err != nil {
			return nil, err
		}
		return p, nil
	case OpFullPageImage:
		var p FullPageImagePayload
		if err := json.Unmarshal(data, &p); err != nil {
			return nil, err
		}
		return p, nil
	default:
		// Unknown op type — return raw JSON bytes
		var v interface{}
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, err
		}
		return v, nil
	}
}

func decodeBinaryPayload(data []byte, opType byte) (interface{}, error) {
	switch opType {
	case OpPageInsert:
		return decodePageInsertBinary(data)
	case OpPageDelete, OpPageUpdateXMax:
		return decodePageDeleteBinary(data)
	case OpSchemaWrite:
		return decodeSchemaWriteBinary(data)
	case OpVacuumBegin, OpVacuumCommit:
		return decodeVacuumBinary(data)
	case OpRewriteBegin, OpRewriteCommit, OpRewriteData:
		return decodeRewriteBinary(data)
	case OpTruncateTable:
		return decodeTruncateTableBinary(data)
	case OpCheckpoint:
		return decodeCheckpointBinary(data)
	case OpFullPageImage:
		return decodeFullPageImageBinary(data)
	default:
		return nil, fmt.Errorf("wal: unknown op type 0x%02x for binary payload", opType)
	}
}

// --- Page Insert ---

func encodePageInsertBinary(p WALPageInsertPayload) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte(binaryPayloadMarker)
	writeLPString(&buf, p.DB)
	writeLPString(&buf, p.Table)
	binary.Write(&buf, binary.LittleEndian, p.SegmentNo)
	binary.Write(&buf, binary.LittleEndian, p.PageNo)
	binary.Write(&buf, binary.LittleEndian, p.SlotNo)
	binary.Write(&buf, binary.LittleEndian, p.XID)
	binary.Write(&buf, binary.LittleEndian, uint32(len(p.TupleData)))
	buf.Write(p.TupleData)
	return buf.Bytes(), nil
}

func decodePageInsertBinary(data []byte) (WALPageInsertPayload, error) {
	var p WALPageInsertPayload
	r := bytes.NewReader(data)
	p.DB = readLPString(r)
	p.Table = readLPString(r)
	binary.Read(r, binary.LittleEndian, &p.SegmentNo)
	binary.Read(r, binary.LittleEndian, &p.PageNo)
	binary.Read(r, binary.LittleEndian, &p.SlotNo)
	binary.Read(r, binary.LittleEndian, &p.XID)
	var tupleLen uint32
	binary.Read(r, binary.LittleEndian, &tupleLen)
	if tupleLen > 0 {
		p.TupleData = make([]byte, tupleLen)
		io.ReadFull(r, p.TupleData)
	}
	return p, nil
}

// --- Page Delete ---

func encodePageDeleteBinary(p WALPageDeletePayload) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte(binaryPayloadMarker)
	writeLPString(&buf, p.DB)
	writeLPString(&buf, p.Table)
	binary.Write(&buf, binary.LittleEndian, p.SegmentNo)
	binary.Write(&buf, binary.LittleEndian, p.PageNo)
	binary.Write(&buf, binary.LittleEndian, p.SlotNo)
	binary.Write(&buf, binary.LittleEndian, p.XMax)
	return buf.Bytes(), nil
}

func decodePageDeleteBinary(data []byte) (WALPageDeletePayload, error) {
	var p WALPageDeletePayload
	r := bytes.NewReader(data)
	p.DB = readLPString(r)
	p.Table = readLPString(r)
	binary.Read(r, binary.LittleEndian, &p.SegmentNo)
	binary.Read(r, binary.LittleEndian, &p.PageNo)
	binary.Read(r, binary.LittleEndian, &p.SlotNo)
	binary.Read(r, binary.LittleEndian, &p.XMax)
	return p, nil
}

// --- Schema Write ---

func encodeSchemaWriteBinary(p WALSchemaWritePayload) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte(binaryPayloadMarker)
	writeLPString(&buf, p.DB)
	writeLPString(&buf, p.Table)
	writeLPString(&buf, p.Schema)
	return buf.Bytes(), nil
}

func decodeSchemaWriteBinary(data []byte) (WALSchemaWritePayload, error) {
	var p WALSchemaWritePayload
	r := bytes.NewReader(data)
	p.DB = readLPString(r)
	p.Table = readLPString(r)
	p.Schema = readLPString(r)
	return p, nil
}

// --- Vacuum ---

func encodeVacuumBinary(p WALVacuumPayload) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte(binaryPayloadMarker)
	writeLPString(&buf, p.DB)
	writeLPString(&buf, p.Table)
	writeLPString(&buf, p.ShadowPath)
	return buf.Bytes(), nil
}

func decodeVacuumBinary(data []byte) (WALVacuumPayload, error) {
	var p WALVacuumPayload
	r := bytes.NewReader(data)
	p.DB = readLPString(r)
	p.Table = readLPString(r)
	p.ShadowPath = readLPString(r)
	return p, nil
}

// --- Rewrite ---

func encodeRewriteBinary(p WALRewritePayload) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte(binaryPayloadMarker)
	writeLPString(&buf, p.DB)
	writeLPString(&buf, p.Table)
	return buf.Bytes(), nil
}

func decodeRewriteBinary(data []byte) (WALRewritePayload, error) {
	var p WALRewritePayload
	r := bytes.NewReader(data)
	p.DB = readLPString(r)
	p.Table = readLPString(r)
	return p, nil
}

// --- Truncate Table ---

func encodeTruncateTableBinary(p WALTruncateTablePayload) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte(binaryPayloadMarker)
	writeLPString(&buf, p.DB)
	writeLPString(&buf, p.Table)
	return buf.Bytes(), nil
}

func decodeTruncateTableBinary(data []byte) (WALTruncateTablePayload, error) {
	var p WALTruncateTablePayload
	r := bytes.NewReader(data)
	p.DB = readLPString(r)
	p.Table = readLPString(r)
	return p, nil
}

// --- Checkpoint ---

func encodeCheckpointBinary(p CheckpointPayload) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte(binaryPayloadMarker)
	binary.Write(&buf, binary.LittleEndian, p.LSN)
	return buf.Bytes(), nil
}

func decodeCheckpointBinary(data []byte) (CheckpointPayload, error) {
	var p CheckpointPayload
	r := bytes.NewReader(data)
	binary.Read(r, binary.LittleEndian, &p.LSN)
	return p, nil
}

// --- Full Page Image ---

func encodeFullPageImageBinary(p FullPageImagePayload) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte(binaryPayloadMarker)
	writeLPString(&buf, p.DB)
	writeLPString(&buf, p.Table)
	binary.Write(&buf, binary.LittleEndian, p.SegmentNo)
	binary.Write(&buf, binary.LittleEndian, p.PageNo)
	binary.Write(&buf, binary.LittleEndian, uint32(len(p.PageData)))
	buf.Write(p.PageData)
	return buf.Bytes(), nil
}

func decodeFullPageImageBinary(data []byte) (FullPageImagePayload, error) {
	var p FullPageImagePayload
	r := bytes.NewReader(data)
	p.DB = readLPString(r)
	p.Table = readLPString(r)
	binary.Read(r, binary.LittleEndian, &p.SegmentNo)
	binary.Read(r, binary.LittleEndian, &p.PageNo)
	var pageLen uint32
	binary.Read(r, binary.LittleEndian, &pageLen)
	if pageLen > 0 {
		p.PageData = make([]byte, pageLen)
		io.ReadFull(r, p.PageData)
	}
	return p, nil
}

// --- Helpers: length-prefixed string ---

func writeLPString(w *bytes.Buffer, s string) {
	binary.Write(w, binary.LittleEndian, uint16(len(s)))
	w.WriteString(s)
}

func readLPString(r *bytes.Reader) string {
	var l uint16
	binary.Read(r, binary.LittleEndian, &l)
	if l == 0 {
		return ""
	}
	buf := make([]byte, l)
	io.ReadFull(r, buf)
	return string(buf)
}

type WAL struct {
	file          *os.File
	mu            sync.Mutex
	nextTxID      atomic.Uint64
	path          string
	syncCounter   int
	SyncBatchSize int                       // number of writes between fsyncs (0 = sync every write)
	OnAppend      func()                    // called after each successful WAL append (for metrics)
	em            *crypto.EncryptionManager // nil = no encryption
	groupCommit   *GroupCommit              // nil = no grouping
	writeBehind   *WriteBehindBuffer        // nil = no write-behind batching
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
		SyncBatchSize: 64,
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

	payloadBytes, err := EncodeWALPayloadBinary(payload)
	if err != nil {
		return 0, fmt.Errorf("wal: marshal payload: %w", err)
	}
	return w.appendBytesLocked(opType, payloadBytes)
}

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

func (w *WAL) Close() error {
	// Stop write-behind worker first (drains pending records).
	if w.writeBehind != nil {
		w.writeBehind.Close()
		w.writeBehind = nil
	}

	// Stop group commit worker (drains pending records).
	// Wait for the flush worker goroutine to finish before acquiring w.mu,
	// because doFlush() acquires w.mu internally.
	if w.groupCommit != nil {
		w.groupCommit.Close()
		w.groupCommit = nil
	}

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

// SetEncryptionManager enables WAL encryption. Pass nil to disable.
func (w *WAL) SetEncryptionManager(em *crypto.EncryptionManager) {
	w.em = em
}

// AppendWithTx writes a WAL entry with the given txID (does not auto-increment).
func (w *WAL) AppendWithTx(txID uint64, opType byte, payload interface{}) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	payloadBytes, err := EncodeWALPayloadBinary(payload)
	if err != nil {
		return 0, fmt.Errorf("wal: marshal payload: %w", err)
	}
	return w.appendBytesLockedWithTx(txID, opType, payloadBytes)
}

// WriteFullPageImage writes a full page image to WAL for torn page protection.
// Called BEFORE modifying the page on disk.
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
	payloadBytes, err := EncodeWALPayloadBinary(payload)
	if err != nil {
		return fmt.Errorf("wal: marshal full page image: %w", err)
	}
	_, err = w.appendBytesLockedWithTx(txID, OpFullPageImage, payloadBytes)
	return err
}

func (w *WAL) appendBytesLockedWithTx(txID uint64, opType byte, payload []byte) (uint64, error) {
	record, err := buildRecord(txID, opType, payload, w.em)
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

	if w.OnAppend != nil {
		w.OnAppend()
	}

	return txID, nil
}

func (w *WAL) appendBytesLocked(opType byte, payload []byte) (uint64, error) {
	txID := w.nextTxID.Add(1)

	record, err := buildRecord(txID, opType, payload, w.em)
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

	if w.OnAppend != nil {
		w.OnAppend()
	}

	return txID, nil
}

// writeRecordRaw writes a pre-built record to the WAL file.
// Caller must hold w.mu. Does NOT sync — the caller manages sync policy.
func (w *WAL) writeRecordRaw(rec WALRecord) error {
	if _, err := w.file.Write(rec.Data); err != nil {
		return fmt.Errorf("wal: write record: %w", err)
	}
	if rec.TxID >= w.nextTxID.Load() {
		w.nextTxID.Store(rec.TxID + 1)
	}
	if w.OnAppend != nil {
		w.OnAppend()
	}
	return nil
}

// EnableGroupCommit installs a group commit worker on this WAL.
// batchSize: number of records to accumulate before flushing (0 = disabled).
// batchTime: maximum latency before a partial batch is flushed.
// Panics if called twice or if batchSize <= 0.
func (w *WAL) EnableGroupCommit(batchSize int, batchTime time.Duration) {
	if batchSize <= 0 {
		return
	}
	if w.groupCommit != nil {
		panic("wal: EnableGroupCommit called twice")
	}
	w.groupCommit = NewGroupCommit(w, batchSize, batchTime)
}

// DisableGroupCommit flushes pending records and stops the group commit worker.
func (w *WAL) DisableGroupCommit() {
	if w.groupCommit != nil {
		w.groupCommit.Close()
		w.groupCommit = nil
	}
}

// EnableWriteBehind installs a write-behind buffer on this WAL.
// maxBuffer: number of records to accumulate before triggering a flush.
// flushInterval: maximum time between flushes.
// Panics if called twice or if maxBuffer <= 0.
func (w *WAL) EnableWriteBehind(maxBuffer int, flushInterval time.Duration) {
	if maxBuffer <= 0 {
		return
	}
	if w.writeBehind != nil {
		panic("wal: EnableWriteBehind called twice")
	}
	w.writeBehind = NewWriteBehindBuffer(w, maxBuffer, flushInterval)
}

// DisableWriteBehind flushes pending records and stops the write-behind worker.
func (w *WAL) DisableWriteBehind() {
	if w.writeBehind != nil {
		w.writeBehind.Close()
		w.writeBehind = nil
	}
}

// AppendWithWriteBehind queues a WAL record for batched writing via the
// write-behind buffer. If write-behind is not enabled, falls back to
// AppendWithTx (synchronous write).
func (w *WAL) AppendWithWriteBehind(xid uint64, opType byte, payload interface{}) (uint64, error) {
	if w.writeBehind == nil {
		return w.AppendWithTx(xid, opType, payload)
	}

	payloadBytes, err := EncodeWALPayloadBinary(payload)
	if err != nil {
		return 0, fmt.Errorf("wal: marshal payload: %w", err)
	}

	data, err := buildRecord(xid, opType, payloadBytes, w.em)
	if err != nil {
		return 0, err
	}

	rec := &WALRecord{
		TxID: xid,
		Data: data,
	}

	w.writeBehind.Append(rec)
	return xid, nil
}

// BuildRecord serializes a WAL entry without writing it. Useful for pre-building
// records that will be fed into GroupCommit.AppendBatch.
func BuildRecord(txID uint64, opType byte, payload []byte, em *crypto.EncryptionManager) ([]byte, error) {
	return buildRecord(txID, opType, payload, em)
}

// NextTxID returns the next txID that will be assigned.
func (w *WAL) NextTxID() uint64 {
	return w.nextTxID.Load() + 1
}

// GroupCommit batches multiple WAL writes and performs a single fsync per batch,
// amortizing the cost of expensive fsync syscalls. This mirrors PostgreSQL's
// group commit strategy for 2-3x INSERT throughput improvement.
type GroupCommit struct {
	wal       *WAL
	pending   []*WALRecord
	mu        sync.Mutex
	batchSize int
	batchTime time.Duration
	flushCh   chan struct{}
	done      chan struct{}
	stopped   chan struct{} // closed when flushWorker exits
}

// NewGroupCommit creates a group commit worker. The worker runs in a
// background goroutine and flushes pending records on batch size threshold
// or batch time timeout, whichever comes first.
func NewGroupCommit(wal *WAL, batchSize int, batchTime time.Duration) *GroupCommit {
	gc := &GroupCommit{
		wal:       wal,
		pending:   make([]*WALRecord, 0, batchSize),
		batchSize: batchSize,
		batchTime: batchTime,
		flushCh:   make(chan struct{}, 1),
		done:      make(chan struct{}),
		stopped:   make(chan struct{}),
	}
	go gc.flushWorker()
	return gc
}

// AppendBatch queues a pre-built record for batched writing. The record
// will be written to disk and fsynced when the batch is full or the timer fires.
func (gc *GroupCommit) AppendBatch(rec *WALRecord) error {
	gc.mu.Lock()
	gc.pending = append(gc.pending, rec)
	shouldFlush := len(gc.pending) >= gc.batchSize
	gc.mu.Unlock()

	if shouldFlush {
		select {
		case gc.flushCh <- struct{}{}:
		default:
		}
	}
	return nil
}

// Flush forces an immediate flush of all pending records.
func (gc *GroupCommit) Flush() {
	gc.doFlush()
}

func (gc *GroupCommit) flushWorker() {
	defer close(gc.stopped)
	ticker := time.NewTicker(gc.batchTime)
	defer ticker.Stop()

	for {
		select {
		case <-gc.flushCh:
			gc.doFlush()
		case <-ticker.C:
			gc.doFlush()
		case <-gc.done:
			gc.doFlush() // Final flush — ensure durability
			return
		}
	}
}

func (gc *GroupCommit) doFlush() {
	gc.mu.Lock()
	if len(gc.pending) == 0 {
		gc.mu.Unlock()
		return
	}
	batch := gc.pending
	gc.pending = make([]*WALRecord, 0, gc.batchSize)
	gc.mu.Unlock()

	// Write all records in single locked section, then one fsync.
	gc.wal.mu.Lock()
	for _, rec := range batch {
		if err := gc.wal.writeRecordRaw(*rec); err != nil {
			gc.wal.mu.Unlock()
			slog.Error("wal group commit: write failed", "txID", rec.TxID, "error", err)
			return
		}
	}
	if err := gc.wal.file.Sync(); err != nil {
		gc.wal.mu.Unlock()
		slog.Error("wal group commit: sync failed", "error", err)
		return
	}
	gc.wal.mu.Unlock()
}

// Close signals the flush worker to stop, performs a final flush, and waits
// for the worker goroutine to exit.
func (gc *GroupCommit) Close() {
	close(gc.done)
	<-gc.stopped
}

// buildRecord creates a WAL record.
// Layout: magic(4) + txID(8) + opType(1) + encrypted(1) + keyVersion(4) + nonce(12) + payloadLen(4) + payload + crc(4)
// When not encrypted, keyVersion=0, nonce=12 zero bytes.
func buildRecord(txID uint64, opType byte, payload []byte, enc *crypto.EncryptionManager) ([]byte, error) {
	if len(payload) > maxPayloadSize {
		return nil, fmt.Errorf("wal: payload too large (%d bytes)", len(payload))
	}

	isEncrypted := enc != nil
	var nonce [12]byte
	var keyVersion uint32

	if isEncrypted {
		// Build AAD from txID (8 bytes)
		aad := make([]byte, 8)
		binary.LittleEndian.PutUint64(aad, txID)

		var err error
		n, ciphertext, err := enc.EncryptPage(payload, aad)
		if err != nil {
			return nil, fmt.Errorf("wal: encrypt payload: %w", err)
		}
		copy(nonce[:], n)
		keyVersion = enc.KeyVersion()
		payload = ciphertext
	}

	// Fixed layout: magic(4) + txID(8) + opType(1) + encrypted(1) + keyVersion(4) + nonce(12) + payloadLen(4) + payload + crc(4)
	fixedLen := 4 + 8 + 1 + 1 + 4 + 12 + 4   // 34 bytes header
	recordLen := fixedLen + len(payload) + 4 // +4 for CRC
	record := make([]byte, recordLen)

	copy(record[0:4], recordMagic)
	binary.LittleEndian.PutUint64(record[4:12], txID)
	record[12] = opType
	if isEncrypted {
		record[13] = 1
	} else {
		record[13] = 0
	}
	binary.LittleEndian.PutUint32(record[14:18], keyVersion)
	copy(record[18:30], nonce[:])
	binary.LittleEndian.PutUint32(record[30:34], uint32(len(payload)))
	copy(record[34:34+len(payload)], payload)

	// CRC32 over everything except the CRC field itself
	crc := crc32.ChecksumIEEE(record[:34+len(payload)])
	binary.LittleEndian.PutUint32(record[34+len(payload):], crc)

	return record, nil
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

// Flush syncs WAL file to disk and returns current LSN.
func (w *WAL) Flush() (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.file.Sync(); err != nil {
		return 0, fmt.Errorf("wal flush: %w", err)
	}

	return w.nextTxID.Load(), nil
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
