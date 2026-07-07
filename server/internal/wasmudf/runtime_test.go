package wasmudf

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// Minimal WASM binary: (module (func (export "execute") (result i32) i32.const 42))
var testWASM = []byte{
	0x00, 0x61, 0x73, 0x6d, // magic
	0x01, 0x00, 0x00, 0x00, // version 1
	0x01, 0x05, // type section, 5 bytes
	0x01,                   // 1 type entry
	0x60, 0x00, 0x01, 0x7f, // (func (result i32))
	0x03, 0x02, // function section, 2 bytes
	0x01, // 1 function
	0x00, // type index 0
	0x07, 0x0b, // export section, 11 bytes
	0x01,                                                       // 1 export
	0x07, 0x65, 0x78, 0x65, 0x63, 0x75, 0x74, 0x65, // "execute"
	0x00, 0x00, // func, index 0
	0x0a, 0x06, // code section, 6 bytes
	0x01,       // 1 function body
	0x04,       // body size 4
	0x00,       // 0 locals (vec of 0 groups)
	0x41, 0x2a, // i32.const 42
	0x0b, // end
}

// WASM binary without "execute" export: (module (func (export "foo") (result i32) i32.const 1))
var testWASMNoExecute = []byte{
	0x00, 0x61, 0x73, 0x6d,
	0x01, 0x00, 0x00, 0x00,
	0x01, 0x05,
	0x01, 0x60, 0x00, 0x01, 0x7f,
	0x03, 0x02,
	0x01, 0x00,
	0x07, 0x07, // export section, 7 bytes
	0x01,
	0x03, 0x66, 0x6f, 0x6f, // "foo"
	0x00, 0x00,
	0x0a, 0x06,
	0x01, 0x04, 0x00, 0x41, 0x01, 0x0b,
}

// --- WASM binary builder helpers ---

func leb128(v uint32) []byte {
	var r []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		r = append(r, b)
		if v == 0 {
			break
		}
	}
	return r
}

// wasmModule builds a WASM binary from structured parts.
type wasmModule struct {
	types    [][]byte  // type section entries
	funcs    []byte    // function section (type indices)
	memory   *uint32   // memory pages (nil = no memory)
	globals  []globDef // global section
	exports  []exportDef
	codes    [][]byte // code bodies (full body bytes including locals)
	dataSegs []dataSeg
	secOrder []byte   // forced section order override
}

type globDef struct {
	valtype byte   // 0x7f = i32
	mutable bool
	init    []byte // const expression
}

type exportDef struct {
	name  string
	kind  byte // 0=func, 1=table, 2=memory, 3=global
	index uint32
}

type dataSeg struct {
	memoryIdx uint32
	offset    []byte // const expression
	data      []byte
}

// funcBody builds a complete code section function body.
// locals is groups of (count, valtype) flattened: e.g. {1, 0x7f} = 1 i32 local.
func funcBody(locals []byte, code []byte) []byte {
	var body bytes.Buffer
	// body size (will be calculated)
	var inner bytes.Buffer
	inner.Write(locals)
	inner.Write(code)
	inner.WriteByte(0x0b) // end
	body.Write(leb128(uint32(inner.Len())))
	body.Write(inner.Bytes())
	return body.Bytes()
}

// noLocals builds a zero-locals declaration.
var noLocals = []byte{0x00}

// oneI32Local builds "1 group, 1 i32 local".
var oneI32Local = []byte{0x01, 0x01, 0x7f}

// twoI32Locals builds "1 group, 2 i32 locals".
var twoI32Locals = []byte{0x01, 0x02, 0x7f}

func (m *wasmModule) build() []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}) // header

	// Section 1: Type
	if len(m.types) > 0 {
		var body bytes.Buffer
		body.WriteByte(byte(len(m.types)))
		for _, t := range m.types {
			body.Write(t)
		}
		writeSection(&buf, 1, body.Bytes())
	}

	// Section 3: Function
	if len(m.funcs) > 0 {
		var body bytes.Buffer
		body.WriteByte(byte(len(m.funcs)))
		body.Write(m.funcs)
		writeSection(&buf, 3, body.Bytes())
	}

	// Section 5: Memory
	if m.memory != nil {
		var body bytes.Buffer
		body.WriteByte(1) // 1 memory
		body.WriteByte(0) // min only
		body.Write(leb128(*m.memory))
		writeSection(&buf, 5, body.Bytes())
	}

	// Section 6: Global
	if len(m.globals) > 0 {
		var body bytes.Buffer
		body.WriteByte(byte(len(m.globals)))
		for _, g := range m.globals {
			body.WriteByte(g.valtype)
			if g.mutable {
				body.WriteByte(0x01)
			} else {
				body.WriteByte(0x00)
			}
			body.Write(g.init)
			body.WriteByte(0x0b) // end
		}
		writeSection(&buf, 6, body.Bytes())
	}

	// Section 7: Export
	if len(m.exports) > 0 {
		var body bytes.Buffer
		body.WriteByte(byte(len(m.exports)))
		for _, e := range m.exports {
			body.Write(leb128(uint32(len(e.name))))
			body.WriteString(e.name)
			body.WriteByte(e.kind)
			body.Write(leb128(e.index))
		}
		writeSection(&buf, 7, body.Bytes())
	}

	// Section 10: Code
	if len(m.codes) > 0 {
		var body bytes.Buffer
		body.WriteByte(byte(len(m.codes)))
		for _, c := range m.codes {
			body.Write(c)
		}
		writeSection(&buf, 10, body.Bytes())
	}

	// Section 11: Data
	if len(m.dataSegs) > 0 {
		var body bytes.Buffer
		body.WriteByte(byte(len(m.dataSegs)))
		for _, d := range m.dataSegs {
			body.Write(leb128(d.memoryIdx))
			body.Write(d.offset)
			body.WriteByte(0x0b)
			body.Write(leb128(uint32(len(d.data))))
			body.Write(d.data)
		}
		writeSection(&buf, 11, body.Bytes())
	}

	return buf.Bytes()
}

func writeSection(buf *bytes.Buffer, id byte, content []byte) {
	buf.WriteByte(id)
	buf.Write(leb128(uint32(len(content))))
	buf.Write(content)
}

// --- Module builders ---

var mem1Page = uint32(1)

var i32Const0 = []byte{0x41, 0x00} // i32.const 0

// buildRichModule builds a WASM module implementing the rich protocol:
// memory(1), alloc(i32)->i32, execute_args(i32,i32)->i32, execute()->i32,
// result_len()->i32, result_copy(i32)->i32
// Uses memory-based bump pointer at offset 0 instead of mutable global
// to avoid wazero v1.12 global initialization bug.
func buildRichModule() []byte {
	resultJSON := []byte(`{"ok":true}`)
	// Bump pointer stored at memory offset 0, result data at offset 128
	bumpStart := uint32(128)
	resultOffset := uint32(128)
	p := &wasmModule{
		types: [][]byte{
			{0x60, 0x01, 0x7f, 0x01, 0x7f}, // (func (param i32) (result i32))  — alloc, result_copy
			{0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7f}, // (func (param i32 i32) (result i32))  — execute_args
			{0x60, 0x00, 0x01, 0x7f},               // (func (result i32))  — execute, result_len
		},
		funcs:  []byte{0, 1, 2, 2, 0}, // alloc:t0, execute_args:t1, execute:t2, result_len:t2, result_copy:t0
		memory: &mem1Page,
		exports: []exportDef{
			{"alloc", 0, 0},
			{"execute_args", 0, 1},
			{"execute", 0, 2},
			{"result_len", 0, 3},
			{"result_copy", 0, 4},
		},
		codes: [][]byte{
			// alloc(size): read bump from mem[0], store new bump at mem[0], return old bump
			// local 0 = param (size), local 1 = temp (old bump)
			funcBody(oneI32Local, []byte{
				0x41, 0x00,       // i32.const 0
				0x28, 0x02, 0x00, // i32.load offset=0 align=2 → old_bump
				0x21, 0x01,       // local.set 1 (save old bump)
				0x41, 0x00,       // i32.const 0 (mem address)
				0x20, 0x01,       // local.get 1 (old bump)
				0x20, 0x00,       // local.get 0 (size)
				0x6a,             // i32.add → new_bump
				0x36, 0x02, 0x00, // i32.store offset=0 align=2 (mem[0] = new_bump)
				0x20, 0x01,       // local.get 1 (old bump) → return value
			}),
			// execute_args: no-op, return 0
			funcBody(noLocals, append(i32Const0)),
			// execute: no-op, return 0
			funcBody(noLocals, append(i32Const0)),
			// result_len: return len(resultJSON)
			funcBody(noLocals, append([]byte{0x41}, leb128(uint32(len(resultJSON)))...)),
			// result_copy(ptr): copy from resultOffset to memory[ptr], return len
			funcBody(oneI32Local, buildCopyFromOffsetBody(resultJSON, resultOffset)),
		},
		dataSegs: []dataSeg{
			// Bump pointer at offset 0, initialized to bumpStart
			{memoryIdx: 0, offset: i32Const0, data: leb128(bumpStart)},
			// Result JSON at resultOffset
			{memoryIdx: 0, offset: append([]byte{0x41}, leb128(resultOffset)...), data: resultJSON},
		},
	}
	return p.build()
}

// buildCopyBody generates WASM code that copies a byte slice to memory[arg0].
func buildCopyBody(data []byte) []byte {
	var code bytes.Buffer
	for i, b := range data {
		// memory[arg0 + i] = b
		code.Write([]byte{0x20, 0x00}) // local.get 0 (ptr arg)
		if i > 0 {
			code.Write([]byte{0x41})
			code.Write(leb128(uint32(i)))
			code.Write([]byte{0x6a}) // i32.add
		}
		code.Write([]byte{0x41})
		code.Write(leb128(uint32(b)))
		code.Write([]byte{0x3a, 0x00, 0x00}) // i32.store8
	}
	// return len
	code.Write([]byte{0x41})
	code.Write(leb128(uint32(len(data))))
	return code.Bytes()
}

// buildCopyFromOffsetBody generates WASM code that copies data from a fixed memory
// offset to memory[arg0], then returns the length.
func buildCopyFromOffsetBody(data []byte, srcOffset uint32) []byte {
	var code bytes.Buffer
	for i := range data {
		// dest = arg0 + i
		code.Write([]byte{0x20, 0x00}) // local.get 0 (dest ptr)
		if i > 0 {
			code.Write([]byte{0x41})
			code.Write(leb128(uint32(i)))
			code.Write([]byte{0x6a}) // i32.add
		}
		// src = srcOffset + i
		code.Write([]byte{0x41})
		code.Write(leb128(srcOffset + uint32(i)))
		code.Write([]byte{0x2d, 0x00, 0x00}) // i32.load8_u offset=0 align=1
		code.Write([]byte{0x3a, 0x00, 0x00}) // i32.store8
	}
	// return len
	code.Write([]byte{0x41})
	code.Write(leb128(uint32(len(data))))
	return code.Bytes()
}

// buildRichModuleNoAlloc: has execute, result_len, result_copy but no alloc.
func buildRichModuleNoAlloc() []byte {
	resultJSON := []byte(`{"ok":true}`)
	p := &wasmModule{
		types: [][]byte{
			{0x60, 0x00, 0x01, 0x7f},               // (func (result i32))
			{0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7f}, // (func (param i32 i32) (result i32))
		},
		funcs:  []byte{0, 0, 1}, // execute:t0, result_len:t0, result_copy:t1
		memory: &mem1Page,
		globals: []globDef{
			{valtype: 0x7f, mutable: true, init: i32Const0},
		},
		exports: []exportDef{
			{"execute", 0, 0},
			{"result_len", 0, 1},
			{"result_copy", 0, 2},
		},
		codes: [][]byte{
			funcBody(noLocals, append(i32Const0)),
			funcBody(noLocals, append([]byte{0x41}, leb128(uint32(len(resultJSON)))...)),
			funcBody(oneI32Local, buildCopyBody(resultJSON)),
		},
		dataSegs: []dataSeg{
			{memoryIdx: 0, offset: i32Const0, data: resultJSON},
		},
	}
	return p.build()
}

// buildRichModuleZeroResult: result_len returns 0.
func buildRichModuleZeroResult() []byte {
	p := &wasmModule{
		types: [][]byte{
			{0x60, 0x00, 0x01, 0x7f}, // (func (result i32))
		},
		funcs:  []byte{0, 0}, // execute:t0, result_len:t0
		memory: &mem1Page,
		globals: []globDef{
			{valtype: 0x7f, mutable: true, init: i32Const0},
		},
		exports: []exportDef{
			{"execute", 0, 0},
			{"result_len", 0, 1},
		},
		codes: [][]byte{
			funcBody(noLocals, append(i32Const0)),
			funcBody(noLocals, append(i32Const0)),
		},
	}
	return p.build()
}

// buildRichModuleBadJSON: result_copy writes invalid JSON.
func buildRichModuleBadJSON() []byte {
	badJSON := []byte(`{invalid}`)
	p := &wasmModule{
		types: [][]byte{
			{0x60, 0x00, 0x01, 0x7f},
			{0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7f},
		},
		funcs:  []byte{0, 0, 1},
		memory: &mem1Page,
		globals: []globDef{
			{valtype: 0x7f, mutable: true, init: i32Const0},
		},
		exports: []exportDef{
			{"execute", 0, 0},
			{"result_len", 0, 1},
			{"result_copy", 0, 2},
		},
		codes: [][]byte{
			funcBody(noLocals, append(i32Const0)),
			funcBody(noLocals, append([]byte{0x41}, leb128(uint32(len(badJSON)))...)),
			funcBody(oneI32Local, buildCopyBody(badJSON)),
		},
		dataSegs: []dataSeg{
			{memoryIdx: 0, offset: i32Const0, data: badJSON},
		},
	}
	return p.build()
}

// buildRichModuleAllocError: alloc traps, result_len returns 5.
func buildRichModuleAllocError() []byte {
	p := &wasmModule{
		types: [][]byte{
			{0x60, 0x00, 0x01, 0x7f},
			{0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7f},
		},
		funcs:  []byte{0, 0, 0, 1}, // alloc(t0), execute(t0), result_len(t0), result_copy(t1)
		memory: &mem1Page,
		globals: []globDef{
			{valtype: 0x7f, mutable: true, init: i32Const0},
		},
		exports: []exportDef{
			{"alloc", 0, 0},
			{"execute", 0, 1},
			{"result_len", 0, 2},
			{"result_copy", 0, 3},
		},
		codes: [][]byte{
			// alloc traps
			funcBody(noLocals, []byte{0x00}), // unreachable
			// execute returns 0
			funcBody(noLocals, append(i32Const0)),
			// result_len returns 5
			funcBody(noLocals, []byte{0x41, 0x05}),
			// result_copy stub (won't be reached)
			funcBody(oneI32Local, []byte{0x41, 0x00}),
		},
	}
	return p.build()
}

func writeTempWASM(t *testing.T, data []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wasm")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// --- Config tests ---

func TestParseOptions(t *testing.T) {
	opts := map[string]string{
		"memory_limit": "256MB",
		"timeout":      "5s",
	}
	memLimit, timeout, err := ParseOptions(opts)
	if err != nil {
		t.Fatalf("ParseOptions: %v", err)
	}
	if memLimit != 256*1024*1024 {
		t.Errorf("memory_limit = %d, want %d", memLimit, 256*1024*1024)
	}
	if timeout != 5*time.Second {
		t.Errorf("timeout = %v, want 5s", timeout)
	}
}

func TestParseOptionsEmpty(t *testing.T) {
	memLimit, timeout, err := ParseOptions(nil)
	if err != nil {
		t.Fatalf("ParseOptions(nil): %v", err)
	}
	if memLimit != 0 {
		t.Errorf("memory_limit = %d, want 0", memLimit)
	}
	if timeout != 0 {
		t.Errorf("timeout = %v, want 0", timeout)
	}
}

func TestParseOptionsInvalidTimeout(t *testing.T) {
	opts := map[string]string{"timeout": "not-a-duration"}
	_, _, err := ParseOptions(opts)
	if err == nil {
		t.Fatal("expected error for invalid timeout")
	}
}

func TestParseOptionsInvalidMemoryLimit(t *testing.T) {
	opts := map[string]string{"memory_limit": "abc"}
	_, _, err := ParseOptions(opts)
	if err == nil {
		t.Fatal("expected error for invalid memory_limit")
	}
}

func TestParseOptionsCaseInsensitive(t *testing.T) {
	opts := map[string]string{
		"MEMORY_LIMIT": "128MB",
		"TIMEOUT":      "10s",
	}
	memLimit, timeout, err := ParseOptions(opts)
	if err != nil {
		t.Fatalf("ParseOptions: %v", err)
	}
	if memLimit != 128*1024*1024 {
		t.Errorf("memory_limit = %d, want %d", memLimit, 128*1024*1024)
	}
	if timeout != 10*time.Second {
		t.Errorf("timeout = %v, want 10s", timeout)
	}
}

func TestParseOptionsUnknownKey(t *testing.T) {
	opts := map[string]string{"unknown_key": "value"}
	memLimit, timeout, err := ParseOptions(opts)
	if err != nil {
		t.Fatalf("ParseOptions: %v", err)
	}
	if memLimit != 0 || timeout != 0 {
		t.Errorf("unexpected values for unknown key: memLimit=%d, timeout=%v", memLimit, timeout)
	}
}

func TestParseMemoryLimit(t *testing.T) {
	tests := []struct {
		input string
		want  uint32
	}{
		{"256MB", 256 * 1024 * 1024},
		{"1KB", 1024},
		{"1GB", 1024 * 1024 * 1024},
		{"4096", 4096},
		{"  128MB  ", 128 * 1024 * 1024},
		{"0", 0},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseMemoryLimit(tt.input)
			if err != nil {
				t.Fatalf("ParseMemoryLimit(%q): %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParseMemoryLimit(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseMemoryLimitInvalid(t *testing.T) {
	_, err := ParseMemoryLimit("abc")
	if err == nil {
		t.Fatal("expected error for invalid memory limit")
	}
	_, err = ParseMemoryLimit("999TB")
	if err == nil {
		t.Fatal("expected error for unsupported unit")
	}
}

func TestParseMemoryLimitInvalidKB(t *testing.T) {
	_, err := ParseMemoryLimit("abcKB")
	if err == nil {
		t.Fatal("expected error for invalid KB value")
	}
}

func TestParseMemoryLimitInvalidGB(t *testing.T) {
	_, err := ParseMemoryLimit("abcGB")
	if err == nil {
		t.Fatal("expected error for invalid GB value")
	}
}

func TestParseMemoryLimitInvalidMB(t *testing.T) {
	_, err := ParseMemoryLimit("abcMB")
	if err == nil {
		t.Fatal("expected error for invalid MB value")
	}
}

func TestParseMemoryLimitKB(t *testing.T) {
	got, err := ParseMemoryLimit("512KB")
	if err != nil {
		t.Fatalf("ParseMemoryLimit: %v", err)
	}
	if got != 512*1024 {
		t.Errorf("got %d, want %d", got, 512*1024)
	}
}

func TestParseMemoryLimitGB(t *testing.T) {
	got, err := ParseMemoryLimit("2GB")
	if err != nil {
		t.Fatalf("ParseMemoryLimit: %v", err)
	}
	if got != 2*1024*1024*1024 {
		t.Errorf("got %d, want %d", got, 2*1024*1024*1024)
	}
}

func TestParseMemoryLimitLowerCase(t *testing.T) {
	got, err := ParseMemoryLimit("128mb")
	if err != nil {
		t.Fatalf("ParseMemoryLimit: %v", err)
	}
	if got != 128*1024*1024 {
		t.Errorf("got %d, want %d", got, 128*1024*1024)
	}
}

// --- Runtime tests ---

func TestNewRuntime(t *testing.T) {
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()
}

func TestLoadValidModule(t *testing.T) {
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	path := writeTempWASM(t, testWASM)
	fn, err := rt.LoadModule(path)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	if fn == nil {
		t.Fatal("expected non-nil WASMFunction")
	}
}

func TestLoadInvalidFile(t *testing.T) {
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	_, err = rt.LoadModule("/nonexistent/path.wasm")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestLoadInvalidWASM(t *testing.T) {
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	path := writeTempWASM(t, []byte{0x00, 0x01, 0x02})
	_, err = rt.LoadModule(path)
	if err == nil {
		t.Fatal("expected error for invalid WASM binary")
	}
}

func TestCallExecute(t *testing.T) {
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	path := writeTempWASM(t, testWASM)
	fn, err := rt.LoadModule(path)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}

	result, err := fn.Call(context.Background(), nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	val, ok := result.(uint64)
	if !ok {
		t.Fatalf("result type = %T, want uint64", result)
	}
	if val != 42 {
		t.Errorf("result = %d, want 42", val)
	}
}

func TestCallNoExecuteExport(t *testing.T) {
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	// testWASMNoExecute exports "foo" (not in allowed list) and lacks "execute".
	// LoadModule should reject it due to unexpected export.
	path := writeTempWASM(t, testWASMNoExecute)
	_, err = rt.LoadModule(path)
	if err == nil {
		t.Fatal("expected error for module with unexpected export")
	}
}

func TestCallWithTimeout(t *testing.T) {
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	path := writeTempWASM(t, testWASM)
	fn, err := rt.LoadModule(path)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	fn.Timeout = 5 * time.Second

	result, err := fn.Call(context.Background(), nil)
	if err != nil {
		t.Fatalf("Call with timeout: %v", err)
	}
	val, ok := result.(uint64)
	if !ok {
		t.Fatalf("result type = %T, want uint64", result)
	}
	if val != 42 {
		t.Errorf("result = %d, want 42", val)
	}
}

func TestCallWithArgs(t *testing.T) {
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	path := writeTempWASM(t, testWASM)
	fn, err := rt.LoadModule(path)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}

	// testWASM has no alloc export — passing args should return an error.
	_, err = fn.Call(context.Background(), []interface{}{"hello", 42})
	if err == nil {
		t.Fatal("expected error when passing args to module without alloc export")
	}
}

func TestCallWithEmptyArgs(t *testing.T) {
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	path := writeTempWASM(t, testWASM)
	fn, err := rt.LoadModule(path)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}

	result, err := fn.Call(context.Background(), []interface{}{})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	val, ok := result.(uint64)
	if !ok {
		t.Fatalf("result type = %T, want uint64", result)
	}
	if val != 42 {
		t.Errorf("result = %d, want 42", val)
	}
}

// --- Rich protocol tests ---

func TestCallRichProtocol(t *testing.T) {
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	path := writeTempWASM(t, buildRichModule())
	fn, err := rt.LoadModule(path)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}

	result, err := fn.Call(context.Background(), nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// Should be {"ok":true} parsed as map
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("result type = %T, want map[string]interface{}", result)
	}
	if v, ok := m["ok"]; !ok || v != true {
		t.Errorf("result[ok] = %v, want true", v)
	}
}

func TestCallRichProtocolNoArgs(t *testing.T) {
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	path := writeTempWASM(t, buildRichModule())
	fn, err := rt.LoadModule(path)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}

	// Call with nil args — should still work
	result, err := fn.Call(context.Background(), nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("result type = %T, want map[string]interface{}", result)
	}
	if v, ok := m["ok"]; !ok || v != true {
		t.Errorf("result[ok] = %v, want true", v)
	}
}

func TestCallRichProtocolNoAlloc(t *testing.T) {
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	path := writeTempWASM(t, buildRichModuleNoAlloc())
	fn, err := rt.LoadModule(path)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}

	// readResult fails because no alloc export, falls back to raw return
	result, err := fn.Call(context.Background(), nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	val, ok := result.(uint64)
	if !ok {
		t.Fatalf("result type = %T, want uint64", result)
	}
	if val != 0 {
		t.Errorf("result = %d, want 0", val)
	}
}

func TestCallRichProtocolZeroLength(t *testing.T) {
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	path := writeTempWASM(t, buildRichModuleZeroResult())
	fn, err := rt.LoadModule(path)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}

	// result_len returns 0, readResult returns nil, falls back to raw
	result, err := fn.Call(context.Background(), nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	val, ok := result.(uint64)
	if !ok {
		t.Fatalf("result type = %T, want uint64", result)
	}
	if val != 0 {
		t.Errorf("result = %d, want 0", val)
	}
}

func TestCallRichProtocolBadJSON(t *testing.T) {
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	path := writeTempWASM(t, buildRichModuleBadJSON())
	fn, err := rt.LoadModule(path)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}

	// result_copy writes invalid JSON, readResult fails to unmarshal
	result, err := fn.Call(context.Background(), nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	// Falls back to raw return value
	val, ok := result.(uint64)
	if !ok {
		t.Fatalf("result type = %T, want uint64", result)
	}
	if val != 0 {
		t.Errorf("result = %d, want 0", val)
	}
}

func TestCallRichProtocolAllocError(t *testing.T) {
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	path := writeTempWASM(t, buildRichModuleAllocError())
	fn, err := rt.LoadModule(path)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}

	// alloc traps, readResult fails at alloc step
	result, err := fn.Call(context.Background(), nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	val, ok := result.(uint64)
	if !ok {
		t.Fatalf("result type = %T, want uint64", result)
	}
	if val != 0 {
		t.Errorf("result = %d, want 0", val)
	}
}

func TestCallRichProtocolManyArgs(t *testing.T) {
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	path := writeTempWASM(t, buildRichModule())
	fn, err := rt.LoadModule(path)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}

	args := []interface{}{"string", 123, 3.14, true, nil, []interface{}{1, 2, 3}}
	result, err := fn.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestCallReadResultResultCopyTrap(t *testing.T) {
	// Module with result_len>0 but result_copy traps (unreachable)
	p := &wasmModule{
		types: [][]byte{
			{0x60, 0x00, 0x01, 0x7f},
		},
		funcs:  []byte{0, 0, 0}, // execute, result_len, result_copy
		memory: &mem1Page,
		globals: []globDef{
			{valtype: 0x7f, mutable: true, init: i32Const0},
		},
		exports: []exportDef{
			{"execute", 0, 0},
			{"result_len", 0, 1},
			{"result_copy", 0, 2},
		},
		codes: [][]byte{
			funcBody(noLocals, append(i32Const0)),
			funcBody(noLocals, []byte{0x41, 0x05}), // result_len returns 5
			funcBody(noLocals, []byte{0x00}),        // result_copy traps
		},
	}

	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	path := writeTempWASM(t, p.build())
	fn, err := rt.LoadModule(path)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}

	result, err := fn.Call(context.Background(), nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	val, ok := result.(uint64)
	if !ok {
		t.Fatalf("result type = %T, want uint64", result)
	}
	if val != 0 {
		t.Errorf("result = %d, want 0", val)
	}
}

func TestCallNoResults(t *testing.T) {
	// Module whose execute has no return value
	p := &wasmModule{
		types: [][]byte{
			{0x60, 0x00, 0x00}, // (func) — no params, no results
		},
		funcs:  []byte{0},
		exports: []exportDef{{"execute", 0, 0}},
		codes:  [][]byte{funcBody(noLocals, nil)},
	}

	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	path := writeTempWASM(t, p.build())
	fn, err := rt.LoadModule(path)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}

	result, err := fn.Call(context.Background(), nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result, got %v", result)
	}
}

func TestClose(t *testing.T) {
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestCloseTwice(t *testing.T) {
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	rt.Close()
	_ = rt.Close() // may or may not error
}

// --- Unit tests for helpers ---

func TestJSONMarshalArgs(t *testing.T) {
	args := []interface{}{"hello", 42, true, nil}
	data, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var result []interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(result) != 4 {
		t.Errorf("len(result) = %d, want 4", len(result))
	}
}

func TestWASMBuilderValid(t *testing.T) {
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	builders := []struct {
		name string
		fn   func() []byte
	}{
		{"rich", buildRichModule},
		{"no_alloc", buildRichModuleNoAlloc},
		{"zero_result", buildRichModuleZeroResult},
		{"bad_json", buildRichModuleBadJSON},
		{"alloc_error", buildRichModuleAllocError},
	}
	for _, b := range builders {
		t.Run(b.name, func(t *testing.T) {
			path := writeTempWASM(t, b.fn())
			fn, err := rt.LoadModule(path)
			if err != nil {
				t.Fatalf("LoadModule(%s): %v", b.name, err)
			}
			if fn == nil {
				t.Fatal("expected non-nil WASMFunction")
			}
		})
	}
}

func TestNewRuntimeWASIFailure(t *testing.T) {
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)

	_, err := wasi_snapshot_preview1.Instantiate(ctx, r)
	if err != nil {
		t.Fatalf("first WASI instantiate: %v", err)
	}
	// Second WASI instantiation on same runtime — behavior varies by version
	_, _ = wasi_snapshot_preview1.Instantiate(ctx, r)
}

// testWASMNoAlloc has "execute" but no "alloc" — triggers passArgs error path.
var testWASMNoAlloc = []byte{
	0x00, 0x61, 0x73, 0x6d,
	0x01, 0x00, 0x00, 0x00,
	0x01, 0x05,
	0x01, 0x60, 0x00, 0x01, 0x7f,
	0x03, 0x02,
	0x01, 0x00,
	0x07, 0x0b,
	0x01,
	0x07, 0x65, 0x78, 0x65, 0x63, 0x75, 0x74, 0x65,
	0x00, 0x00,
	0x0a, 0x06,
	0x01, 0x04, 0x00, 0x41, 0x2a, 0x0b,
}

func TestCallWithArgsNoAlloc(t *testing.T) {
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	path := writeTempWASM(t, testWASMNoAlloc)
	fn, err := rt.LoadModule(path)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}

	// Module has no alloc export, so passArgs will fail.
	// Since we now propagate passArgs errors, Call should return an error.
	_, err = fn.Call(context.Background(), []interface{}{"hello"})
	if err == nil {
		t.Fatal("expected error when passing args to module without alloc export")
	}
}

func TestCallWithArgsModuleTimeout(t *testing.T) {
	// Test that Call correctly wraps the context with a timeout when
	// Timeout > 0, and that the module still executes successfully within
	// that timeout. wazero compiles WASM to native code, so simple modules
	// complete before the timeout fires — the code path is exercised but
	// the deadline is never exceeded.
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	path := writeTempWASM(t, testWASM)
	fn, err := rt.LoadModule(path)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	fn.Timeout = 1 * time.Millisecond

	result, err := fn.Call(context.Background(), nil)
	if err != nil {
		t.Fatalf("Call with timeout: %v", err)
	}
	val, ok := result.(uint64)
	if !ok {
		t.Fatalf("result type = %T, want uint64", result)
	}
	if val != 42 {
		t.Errorf("result = %d, want 42", val)
	}
}

func TestWASMExecutionAndCleanup(t *testing.T) {
	// Verifies that WASM functions execute correctly and the runtime can be
	// cleaned up after use — the unit-testable aspect of crash recovery for
	// WASM UDFs (actual crash-during-execution scenarios require integration
	// testing with the storage layer).
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	path := writeTempWASM(t, testWASM)
	fn, err := rt.LoadModule(path)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}

	// Execute and verify result
	result, err := fn.Call(context.Background(), nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.(uint64) != 42 {
		t.Errorf("result = %d, want 42", result)
	}

	// Verify runtime can be closed cleanly after execution
	if err := rt.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// --- Security tests ---

func TestUnexpectedExportRejected(t *testing.T) {
	// Module that exports "debugger" — not in the allowed list.
	p := &wasmModule{
		types:    [][]byte{{0x60, 0x00, 0x01, 0x7f}},
		funcs:    []byte{0, 0},
		exports:  []exportDef{{"execute", 0, 0}, {"debugger", 0, 1}},
		codes:    [][]byte{funcBody(noLocals, append(i32Const0)), funcBody(noLocals, append(i32Const0))},
	}

	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	path := writeTempWASM(t, p.build())
	_, err = rt.LoadModule(path)
	if err == nil {
		t.Fatal("expected error for module with unexpected 'debugger' export")
	}
}

func TestNewRuntimeWithLimits(t *testing.T) {
	// Create runtime with 1 page memory limit (64 KB).
	rt, err := NewRuntimeWithLimits(1, 5*time.Second)
	if err != nil {
		t.Fatalf("NewRuntimeWithLimits: %v", err)
	}
	defer rt.Close()

	// Module requesting 1 page should work.
	path := writeTempWASM(t, testWASM)
	fn, err := rt.LoadModule(path)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}

	result, err := fn.Call(context.Background(), nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.(uint64) != 42 {
		t.Errorf("result = %d, want 42", result)
	}
}

func TestDefaultTimeoutApplied(t *testing.T) {
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	// DefaultRuntime should have the default timeout.
	if rt.defaultTimeout != DefaultTimeout {
		t.Errorf("defaultTimeout = %v, want %v", rt.defaultTimeout, DefaultTimeout)
	}
}

func TestLoadModuleRequiresExecuteExport(t *testing.T) {
	// Module with only "alloc" export (no "execute") — should be rejected.
	p := &wasmModule{
		types:    [][]byte{{0x60, 0x01, 0x7f, 0x01, 0x7f}},
		funcs:    []byte{0},
		exports:  []exportDef{{"alloc", 0, 0}},
		codes:    [][]byte{funcBody(oneI32Local, append([]byte{0x20, 0x00}))},
	}

	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	path := writeTempWASM(t, p.build())
	_, err = rt.LoadModule(path)
	if err == nil {
		t.Fatal("expected error for module without 'execute' export")
	}
}

// --- ValidateWASMBytes tests ---

func TestValidateWASMBytesValid(t *testing.T) {
	// testWASM is a valid binary with "execute" export (whitelisted).
	if err := ValidateWASMBytes(testWASM, DefaultMaxModuleSize); err != nil {
		t.Errorf("expected valid WASM to pass validation, got: %v", err)
	}
}

func TestValidateWASMBytesInvalidMagic(t *testing.T) {
	data := make([]byte, len(testWASM))
	copy(data, testWASM)
	data[0] = 0xFF
	if err := ValidateWASMBytes(data, DefaultMaxModuleSize); err == nil {
		t.Fatal("expected error for invalid magic bytes")
	}
}

func TestValidateWASMBytesTooSmall(t *testing.T) {
	if err := ValidateWASMBytes([]byte{0x00, 0x61}, DefaultMaxModuleSize); err == nil {
		t.Fatal("expected error for binary too small")
	}
}

func TestValidateWASMBytesUnsupportedVersion(t *testing.T) {
	data := make([]byte, len(testWASM))
	copy(data, testWASM)
	data[4] = 0x02 // version 2
	if err := ValidateWASMBytes(data, DefaultMaxModuleSize); err == nil {
		t.Fatal("expected error for unsupported version")
	}
}

func TestValidateWASMBytesOversized(t *testing.T) {
	if err := ValidateWASMBytes(testWASM, 4); err == nil {
		t.Fatal("expected error for oversized module")
	}
}

func TestValidateWASMBytesUnexpectedExport(t *testing.T) {
	// ValidateWASMBytes passes binary-level checks for testWASMNoExecute.
	// Export whitelist is enforced by wazero at compile time in LoadModule.
	if err := ValidateWASMBytes(testWASMNoExecute, DefaultMaxModuleSize); err != nil {
		t.Errorf("ValidateWASMBytes should pass binary checks, got: %v", err)
	}
	// But LoadModule should reject it due to unexpected "foo" export.
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()
	path := writeTempWASM(t, testWASMNoExecute)
	_, err = rt.LoadModule(path)
	if err == nil {
		t.Fatal("expected LoadModule to reject module with unexpected 'foo' export")
	}
}

func TestValidateWASMBytesValidExport(t *testing.T) {
	// Module exporting only "execute" — should pass.
	if err := ValidateWASMBytes(testWASM, DefaultMaxModuleSize); err != nil {
		t.Errorf("expected 'execute' export to be accepted, got: %v", err)
	}
}

func TestValidateWASMBytesEmpty(t *testing.T) {
	if err := ValidateWASMBytes(nil, DefaultMaxModuleSize); err == nil {
		t.Fatal("expected error for nil input")
	}
	if err := ValidateWASMBytes([]byte{}, DefaultMaxModuleSize); err == nil {
		t.Fatal("expected error for empty input")
	}
}

// --- decodeLEB128 edge cases ---

func TestDecodeLEB128Zero(t *testing.T) {
	val, n, err := decodeLEB128([]byte{0x00})
	if err != nil {
		t.Fatalf("decodeLEB128(0): %v", err)
	}
	if val != 0 || n != 1 {
		t.Errorf("got (%d, %d), want (0, 1)", val, n)
	}
}

func TestDecodeLEB128OneByte(t *testing.T) {
	val, n, err := decodeLEB128([]byte{0x7f})
	if err != nil {
		t.Fatalf("decodeLEB128(0x7f): %v", err)
	}
	if val != 127 || n != 1 {
		t.Errorf("got (%d, %d), want (127, 1)", val, n)
	}
}

func TestDecodeLEB128TwoBytes(t *testing.T) {
	val, n, err := decodeLEB128([]byte{0x80, 0x01})
	if err != nil {
		t.Fatalf("decodeLEB128(0x80,0x01): %v", err)
	}
	if val != 128 || n != 2 {
		t.Errorf("got (%d, %d), want (128, 2)", val, n)
	}
}

func TestDecodeLEB128Truncated(t *testing.T) {
	// All continuation bits set, but no terminator — should error.
	_, _, err := decodeLEB128([]byte{0x80, 0x80, 0x80, 0x80, 0x80})
	if err == nil {
		t.Fatal("expected error for truncated LEB128")
	}
}

func TestDecodeLEB128MaxUint32(t *testing.T) {
	val, n, err := decodeLEB128([]byte{0xff, 0xff, 0xff, 0xff, 0x0f})
	if err != nil {
		t.Fatalf("decodeLEB128(max uint32): %v", err)
	}
	if val != 0xffffffff || n != 5 {
		t.Errorf("got (%d, %d), want (0xffffffff, 5)", val, n)
	}
}

func TestDecodeLEB128Empty(t *testing.T) {
	_, _, err := decodeLEB128([]byte{})
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

// --- Section size validation edge cases ---

func TestValidateSectionSizesTruncated(t *testing.T) {
	// Build a binary that claims a section size larger than remaining data.
	data := []byte{
		0x00, 0x61, 0x73, 0x6d, // magic
		0x01, 0x00, 0x00, 0x00, // version 1
		0x01, // type section ID
		0xFF, 0xFF, 0xFF, 0xFF, 0x0f, // section size = max uint32 (but data is only a few bytes)
	}
	if err := ValidateWASMBytes(data, DefaultMaxModuleSize); err == nil {
		t.Fatal("expected error for section declaring size larger than remaining data")
	}
}

func TestValidateSectionSizesOversized(t *testing.T) {
	// Section size exactly at the maxModuleSize limit (1 byte) — should fail.
	data := []byte{
		0x00, 0x61, 0x73, 0x6d,
		0x01, 0x00, 0x00, 0x00,
		0x01, // type section ID
		0x02, // LEB128: size = 2
		0x01, 0x00, // 2 bytes of section content
	}
	if err := ValidateWASMBytes(data, 1); err == nil {
		t.Fatal("expected error when section size exceeds maxModuleSize")
	}
}

func TestValidateWASMBytesMultipleExports(t *testing.T) {
	// Module with two valid exports: "alloc" and "execute".
	p := &wasmModule{
		types: [][]byte{
			{0x60, 0x00, 0x01, 0x7f},               // (func (result i32))
			{0x60, 0x01, 0x7f, 0x01, 0x7f}, // (func (param i32) (result i32))
		},
		funcs:  []byte{0, 1}, // alloc:t1, execute:t0
		exports: []exportDef{{"alloc", 0, 0}, {"execute", 0, 1}},
		codes: [][]byte{
			funcBody(oneI32Local, append([]byte{0x20, 0x00})),
			funcBody(noLocals, append(i32Const0)),
		},
	}
	bin := p.build()
	if err := ValidateWASMBytes(bin, DefaultMaxModuleSize); err != nil {
		t.Errorf("expected valid exports to pass, got: %v", err)
	}
}

func TestValidateWASMBytesExportNameTooLong(t *testing.T) {
	// A module with a long export name passes binary validation (no binary-level name check).
	// Export whitelist is enforced by wazero at compile time in LoadModule.
	longName := ""
	for i := 0; i < 260; i++ {
		longName += "a"
	}
	p := &wasmModule{
		types:    [][]byte{{0x60, 0x00, 0x01, 0x7f}},
		funcs:    []byte{0},
		exports:  []exportDef{{longName, 0, 0}},
		codes:    [][]byte{funcBody(noLocals, append(i32Const0))},
	}
	bin := p.build()
	if err := ValidateWASMBytes(bin, DefaultMaxModuleSize); err != nil {
		t.Errorf("ValidateWASMBytes should pass binary checks for long export name, got: %v", err)
	}
	// LoadModule should reject it since the long name is not in the whitelist.
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()
	path := writeTempWASM(t, bin)
	_, err = rt.LoadModule(path)
	if err == nil {
		t.Fatal("expected LoadModule to reject module with non-whitelisted export")
	}
}
