package wal

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestBinaryPayloadRoundtripPageInsert(t *testing.T) {
	original := WALPageInsertPayload{
		DB: "testdb", Table: "users", SegmentNo: 1, PageNo: 100, SlotNo: 5, XID: 42,
		TupleData: []byte("test data here"),
	}
	data, err := EncodeWALPayloadBinary(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeWALPayload(data, OpPageInsert)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := decoded.(WALPageInsertPayload)
	if !ok {
		t.Fatalf("expected WALPageInsertPayload, got %T", decoded)
	}
	if p.DB != original.DB || p.Table != original.Table || p.SegmentNo != original.SegmentNo ||
		p.PageNo != original.PageNo || p.SlotNo != original.SlotNo || p.XID != original.XID ||
		!bytes.Equal(p.TupleData, original.TupleData) {
		t.Errorf("mismatch: got %+v, want %+v", p, original)
	}
}

func TestBinaryPayloadRoundtripPageDelete(t *testing.T) {
	original := WALPageDeletePayload{
		DB: "testdb", Table: "orders", SegmentNo: 2, PageNo: 50, SlotNo: 3, XMax: 99,
	}
	data, err := EncodeWALPayloadBinary(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeWALPayload(data, OpPageDelete)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := decoded.(WALPageDeletePayload)
	if !ok {
		t.Fatalf("expected WALPageDeletePayload, got %T", decoded)
	}
	if p.DB != original.DB || p.Table != original.Table || p.SegmentNo != original.SegmentNo ||
		p.PageNo != original.PageNo || p.SlotNo != original.SlotNo || p.XMax != original.XMax {
		t.Errorf("mismatch: got %+v, want %+v", p, original)
	}
}

func TestBinaryPayloadRoundtripSchemaWrite(t *testing.T) {
	original := WALSchemaWritePayload{
		DB: "mydb", Table: "items", Schema: `{"columns":[{"name":"id","type":"int"}]}`,
	}
	data, err := EncodeWALPayloadBinary(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeWALPayload(data, OpSchemaWrite)
	if err != nil {
		t.Fatal(err)
	}
	p := decoded.(WALSchemaWritePayload)
	if p.DB != original.DB || p.Table != original.Table || p.Schema != original.Schema {
		t.Errorf("mismatch: got %+v, want %+v", p, original)
	}
}

func TestBinaryPayloadRoundtripCheckpoint(t *testing.T) {
	original := CheckpointPayload{LSN: 12345}
	data, err := EncodeWALPayloadBinary(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeWALPayload(data, OpCheckpoint)
	if err != nil {
		t.Fatal(err)
	}
	p := decoded.(CheckpointPayload)
	if p.LSN != original.LSN {
		t.Errorf("LSN mismatch: got %d, want %d", p.LSN, original.LSN)
	}
}

func TestBinaryPayloadRoundtripFullPageImage(t *testing.T) {
	pageData := make([]byte, 8192)
	for i := range pageData {
		pageData[i] = byte(i % 256)
	}
	original := FullPageImagePayload{
		DB: "db1", Table: "t1", SegmentNo: 0, PageNo: 7, PageData: pageData,
	}
	data, err := EncodeWALPayloadBinary(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeWALPayload(data, OpFullPageImage)
	if err != nil {
		t.Fatal(err)
	}
	p := decoded.(FullPageImagePayload)
	if p.DB != original.DB || p.Table != original.Table || p.PageNo != original.PageNo ||
		!bytes.Equal(p.PageData, original.PageData) {
		t.Error("mismatch")
	}
}

func TestBinaryPayloadSizeComparison(t *testing.T) {
	payload := WALPageInsertPayload{
		DB: "mydb", Table: "users", SegmentNo: 1, PageNo: 100, SlotNo: 5, XID: 42,
		TupleData: bytes.Repeat([]byte("x"), 100),
	}
	binaryData, _ := EncodeWALPayloadBinary(payload)
	jsonData, _ := json.Marshal(payload)
	ratio := float64(len(jsonData)) / float64(len(binaryData))
	t.Logf("Binary: %d bytes, JSON: %d bytes, Ratio: %.2fx", len(binaryData), len(jsonData), ratio)
	if ratio < 1.0 {
		t.Errorf("binary should be smaller than JSON, got binary=%d JSON=%d", len(binaryData), len(jsonData))
	}
}

func TestBinaryPayloadSizeComparisonLargeTuple(t *testing.T) {
	payload := WALPageInsertPayload{
		DB: "production", Table: "events", SegmentNo: 3, PageNo: 1024, SlotNo: 127, XID: 999999,
		TupleData: bytes.Repeat([]byte("payload-"), 256),
	}
	binaryData, _ := EncodeWALPayloadBinary(payload)
	jsonData, _ := json.Marshal(payload)
	ratio := float64(len(jsonData)) / float64(len(binaryData))
	t.Logf("Binary: %d bytes, JSON: %d bytes, Ratio: %.2fx", len(binaryData), len(jsonData), ratio)
	if ratio < 1.0 {
		t.Errorf("binary should be smaller than JSON, got binary=%d JSON=%d", len(binaryData), len(jsonData))
	}
}

func TestBinaryPayloadRoundtripViaWAL(t *testing.T) {
	w := mustOpen(t)

	insertPayload := WALPageInsertPayload{
		DB: "testdb", Table: "users", SegmentNo: 1, PageNo: 100, SlotNo: 5, XID: 42,
		TupleData: []byte("hello world"),
	}
	_, err := w.Append(OpPageInsert, insertPayload)
	if err != nil {
		t.Fatal(err)
	}

	deletePayload := WALPageDeletePayload{
		DB: "testdb", Table: "users", SegmentNo: 1, PageNo: 100, SlotNo: 5, XMax: 43,
	}
	_, err = w.Append(OpPageDelete, deletePayload)
	if err != nil {
		t.Fatal(err)
	}

	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 2 {
		t.Fatalf("expected at least 2 entries, got %d", len(entries))
	}

	// First entry is the insert
	decoded1, err := DecodeWALPayload(entries[0].Payload, entries[0].OpType)
	if err != nil {
		t.Fatal(err)
	}
	p1 := decoded1.(WALPageInsertPayload)
	if p1.DB != "testdb" || p1.PageNo != 100 || p1.XID != 42 || !bytes.Equal(p1.TupleData, []byte("hello world")) {
		t.Errorf("insert mismatch: %+v", p1)
	}

	// Find the delete entry
	var foundDelete bool
	for _, e := range entries {
		if e.OpType == OpPageDelete {
			decoded2, err := DecodeWALPayload(e.Payload, e.OpType)
			if err != nil {
				t.Fatal(err)
			}
			p2 := decoded2.(WALPageDeletePayload)
			if p2.DB != "testdb" || p2.XMax != 43 {
				t.Errorf("delete mismatch: %+v", p2)
			}
			foundDelete = true
			break
		}
	}
	if !foundDelete {
		t.Error("no OpPageDelete entry found")
	}
}

func TestLegacyJSONPayloadStillDecodable(t *testing.T) {
	// Simulate a legacy JSON payload (no marker byte)
	legacyPayload := WALPageInsertPayload{
		DB: "legacydb", Table: "old", SegmentNo: 0, PageNo: 1, SlotNo: 0, XID: 1,
		TupleData: []byte("old data"),
	}
	jsonData, err := json.Marshal(legacyPayload)
	if err != nil {
		t.Fatal(err)
	}
	// Decode without marker byte — should use JSON fallback
	decoded, err := DecodeWALPayload(jsonData, OpPageInsert)
	if err != nil {
		t.Fatal(err)
	}
	p := decoded.(WALPageInsertPayload)
	if p.DB != "legacydb" || !bytes.Equal(p.TupleData, []byte("old data")) {
		t.Errorf("legacy decode mismatch: %+v", p)
	}
}

func TestBinaryPayloadEmptyTupleData(t *testing.T) {
	original := WALPageInsertPayload{
		DB: "db", Table: "t", SegmentNo: 0, PageNo: 0, SlotNo: 0, XID: 0,
		TupleData: []byte{},
	}
	data, err := EncodeWALPayloadBinary(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeWALPayload(data, OpPageInsert)
	if err != nil {
		t.Fatal(err)
	}
	p := decoded.(WALPageInsertPayload)
	if len(p.TupleData) != 0 {
		t.Errorf("expected empty tuple data, got %d bytes", len(p.TupleData))
	}
}
