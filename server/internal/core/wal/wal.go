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

// BuildRecord serializes a WAL entry without writing it. Useful for pre-building
// records that will be fed into GroupCommit.AppendBatch.
func BuildRecord(txID uint64, opType byte, payload []byte, em *crypto.EncryptionManager) ([]byte, error) {
	return buildRecord(txID, opType, payload, em)
}

// NextTxID returns the next txID that will be assigned.
func (w *WAL) NextTxID() uint64 {
	return w.nextTxID.Load() + 1
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

// Flush syncs WAL file to disk and returns current LSN.
func (w *WAL) Flush() (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.file.Sync(); err != nil {
		return 0, fmt.Errorf("wal flush: %w", err)
	}

	return w.nextTxID.Load(), nil
}
