package wasmudf

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"time"
	"unicode/utf8"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

const (
	// DefaultMaxMemoryPages limits WASM linear memory to 256 pages (16 MB).
	DefaultMaxMemoryPages uint32 = 256
	// DefaultTimeout is the execution deadline when no timeout is configured.
	DefaultTimeout = 30 * time.Second
	// wasmPageSize is the size of a single WASM memory page in bytes.
	wasmPageSize = 64 * 1024
	// DefaultMaxModuleSize is the default maximum allowed WASM binary size (10 MB).
	DefaultMaxModuleSize = 10 * 1024 * 1024
	// wasmMagic is the WASM binary magic number: \0asm.
	wasmMagic = "\x00asm"
	// maxExportNameLength is the maximum length of a WASM export name.
	maxExportNameLength = 256
)

// allowedExports is the whitelist of function names that WASM UDF modules may export.
var allowedExports = map[string]bool{
	"alloc":        true,
	"execute":      true,
	"execute_args": true,
	"result_len":   true,
	"result_copy":  true,
}

// ValidateWASMBytes performs structural validation of a WASM binary before passing to wazero.
// It checks: magic bytes, total size, and per-section size bounds.
// Export whitelist validation is handled by wazero's ExportedFunctions() in LoadModule.
func ValidateWASMBytes(data []byte, maxModuleSize int64) error {
	// 1. Size check
	if int64(len(data)) > maxModuleSize {
		return fmt.Errorf("WASM module size %d bytes exceeds maximum %d bytes", len(data), maxModuleSize)
	}

	// 2. Magic bytes check: first 4 bytes must be 0x00 0x61 0x73 0x6D ("\0asm")
	if len(data) < 8 {
		return fmt.Errorf("WASM binary too small: %d bytes (minimum 8 for header)", len(data))
	}
	if string(data[0:4]) != wasmMagic {
		return fmt.Errorf("invalid WASM magic bytes: expected %x, got %x", []byte(wasmMagic), data[0:4])
	}

	// 3. Version check: bytes 4-7 must be 0x01 0x00 0x00 0x00 (version 1)
	if data[4] != 0x01 || data[5] != 0x00 || data[6] != 0x00 || data[7] != 0x00 {
		return fmt.Errorf("unsupported WASM version: %d (expected 1)", binary.LittleEndian.Uint32(data[4:8]))
	}

	// 4. Validate section sizes: walk sections and ensure no single section exceeds maxModuleSize
	if err := validateSectionSizes(data[8:], maxModuleSize); err != nil {
		return err
	}

	return nil
}

// validateSectionSizes walks WASM sections and checks each section's declared size.
func validateSectionSizes(data []byte, maxModuleSize int64) error {
	off := 0
	for off < len(data) {
		if off+1 > len(data) {
			break
		}
		sectionID := data[off]
		off++

		sectionSize, n, err := decodeLEB128(data[off:])
		if err != nil {
			return fmt.Errorf("invalid LEB128 in section %d size: %w", sectionID, err)
		}
		off += n

		if int64(sectionSize) > maxModuleSize {
			return fmt.Errorf("WASM section %d size %d bytes exceeds maximum %d bytes", sectionID, sectionSize, maxModuleSize)
		}

		if off+int(sectionSize) > len(data) {
			return fmt.Errorf("WASM section %d declares size %d but only %d bytes remain", sectionID, sectionSize, len(data)-off)
		}
		off += int(sectionSize)
	}
	return nil
}

// validateExportNames parses the WASM export section (section ID 7) and validates
// that export names are valid UTF-8, within length bounds, and in the whitelist.
func validateExportNames(data []byte) error {
	off := 0
	for off < len(data) {
		sectionID := data[off]
		off++

		sectionSize, n, err := decodeLEB128(data[off:])
		if err != nil {
			return nil // not an export section, skip validation
		}
		off += n

		if off+int(sectionSize) > len(data) {
			return nil
		}

		if sectionID != 7 {
			off += int(sectionSize)
			continue
		}

		// Parse export section
		secData := data[off : off+int(sectionSize)]
		soff := 0
		exportCount, n, err := decodeLEB128(secData[soff:])
		if err != nil {
			return fmt.Errorf("invalid export section count: %w", err)
		}
		soff += n

		for i := uint32(0); i < exportCount; i++ {
			if soff >= len(secData) {
				return fmt.Errorf("export section truncated at export %d", i)
			}
			nameLen, n, err := decodeLEB128(secData[soff:])
			if err != nil {
				return fmt.Errorf("invalid export name length at export %d: %w", i, err)
			}
			soff += n

			if int(nameLen) > maxExportNameLength {
				return fmt.Errorf("export name length %d exceeds maximum %d bytes at export %d", nameLen, maxExportNameLength, i)
			}
			if soff+int(nameLen) > len(secData) {
				return fmt.Errorf("export name truncated at export %d", i)
			}
			name := string(secData[soff : soff+int(nameLen)])
			soff += int(nameLen)

			if !utf8.ValidString(name) {
				return fmt.Errorf("export name %q is not valid UTF-8", name)
			}
			if !allowedExports[name] {
				return fmt.Errorf("WASM module has unexpected export %q (allowed: alloc, execute, execute_args, result_len, result_copy)", name)
			}
		}
		return nil
	}
	return nil
}

// decodeLEB128 decodes an unsigned LEB128-encoded uint32 from data.
func decodeLEB128(data []byte) (uint32, int, error) {
	var result uint32
	var shift uint
	for i := 0; i < len(data) && i < 5; i++ {
		b := data[i]
		result |= uint32(b&0x7f) << shift
		if b&0x80 == 0 {
			return result, i + 1, nil
		}
		shift += 7
	}
	return 0, 0, fmt.Errorf("LEB128 too long or truncated")
}

// Runtime manages WASM module compilation and execution.
type Runtime struct {
	runtime        wazero.Runtime
	defaultTimeout time.Duration
	maxModuleSize  int64 // maximum allowed WASM binary size in bytes
}

// NewRuntime creates a new WASM runtime with sensible defaults:
// 16 MB memory limit, no WASI, 30-second execution deadline.
func NewRuntime() (*Runtime, error) {
	return NewRuntimeWithLimits(DefaultMaxMemoryPages, DefaultTimeout)
}

// NewRuntimeWithLimits creates a WASM runtime with explicit memory and timeout limits.
// maxMemoryPages=0 disables the memory limit; timeout=0 uses DefaultTimeout.
func NewRuntimeWithLimits(maxMemoryPages uint32, timeout time.Duration) (*Runtime, error) {
	if timeout == 0 {
		timeout = DefaultTimeout
	}

	ctx := context.Background()
	cfg := wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true) // ensure goroutine cleanup on timeout

	if maxMemoryPages > 0 {
		cfg = cfg.WithMemoryLimitPages(maxMemoryPages)
	}

	r := wazero.NewRuntimeWithConfig(ctx, cfg)

	// WASI is intentionally NOT instantiated. WASM UDF modules must be
	// self-contained and have no host filesystem or process access.

	return &Runtime{runtime: r, defaultTimeout: timeout, maxModuleSize: DefaultMaxModuleSize}, nil
}

// NewRuntimeWithConfig creates a WASM runtime with all limits explicitly set.
func NewRuntimeWithConfig(maxMemoryPages uint32, timeout time.Duration, maxModuleSize int64) (*Runtime, error) {
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	if maxModuleSize <= 0 {
		maxModuleSize = DefaultMaxModuleSize
	}

	ctx := context.Background()
	cfg := wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true)

	if maxMemoryPages > 0 {
		cfg = cfg.WithMemoryLimitPages(maxMemoryPages)
	}

	r := wazero.NewRuntimeWithConfig(ctx, cfg)

	return &Runtime{runtime: r, defaultTimeout: timeout, maxModuleSize: maxModuleSize}, nil
}

// Close releases all runtime resources.
func (rt *Runtime) Close() error {
	return rt.runtime.Close(context.Background())
}

// WASMFunction represents a loaded WASM module ready for execution.
type WASMFunction struct {
	Runtime     *Runtime
	ModulePath  string
	MemoryLimit uint32 // in bytes; 0 = use Runtime default
	Timeout     time.Duration
	compiled    wazero.CompiledModule
}

// LoadModule compiles a WASM module from the given file path and validates exports.
func (rt *Runtime) LoadModule(path string) (*WASMFunction, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read WASM module: %w", err)
	}

	if err := ValidateWASMBytes(data, rt.maxModuleSize); err != nil {
		return nil, fmt.Errorf("validate WASM binary: %w", err)
	}

	ctx := context.Background()
	compiled, err := rt.runtime.CompileModule(ctx, data)
	if err != nil {
		return nil, fmt.Errorf("compile WASM module: %w", err)
	}

	// Validate that the module only exports allowed functions.
	for name, def := range compiled.ExportedFunctions() {
		_ = def // we only need the name for validation
		if !allowedExports[name] {
			return nil, fmt.Errorf("WASM module has unexpected export %q (allowed: alloc, execute, execute_args, result_len, result_copy)", name)
		}
	}

	// Must export "execute" — the minimum contract.
	if _, ok := compiled.ExportedFunctions()["execute"]; !ok {
		return nil, fmt.Errorf("WASM module does not export 'execute' function")
	}

	return &WASMFunction{
		Runtime:    rt,
		ModulePath: path,
		compiled:   compiled,
	}, nil
}

// Call executes the WASM function with the given arguments.
// Arguments and return values are JSON-serialized via WASM memory.
//
// The module contract:
//   - export fn "execute_args"(ptr: i32, len: i32) -> i32
//     Writes JSON-encoded arguments to memory[ptr..ptr+len]. Returns 0 on success.
//   - export fn "execute"() -> i32
//     Runs the function logic.
//   - export fn "result_len"() -> i32
//     Returns the byte length of the result buffer.
//   - export fn "result_copy"(ptr: i32) -> i32
//     Copies the result JSON into memory[ptr..] and returns bytes written.
//
// For simpler modules that only export "execute" -> i32, returns the raw i32 value.
func (f *WASMFunction) Call(ctx context.Context, args []interface{}) (interface{}, error) {
	// Always apply a timeout — use per-function, then runtime default, then global default.
	timeout := f.Timeout
	if timeout == 0 {
		timeout = f.Runtime.defaultTimeout
	}
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, timeout)
	defer cancel()

	// Configure memory limit via module config if the function specifies one.
	cfg := wazero.NewModuleConfig().
		WithStdout(nil).
		WithStderr(nil).
		WithStdin(nil).
		WithName("") // allow multiple instantiations

	// If per-function memory limit is set, create a runtime with that limit.
	// However, since we can't change the runtime's memory limit after creation,
	// we rely on the runtime-level limit (set in NewRuntimeWithLimits).
	// The per-function MemoryLimit field is advisory — the runtime limit is enforced.

	mod, err := f.Runtime.runtime.InstantiateModule(ctx, f.compiled, cfg)
	if err != nil {
		return nil, fmt.Errorf("instantiate WASM module: %w", err)
	}
	defer mod.Close(ctx)

	// Serialize arguments to JSON and pass to WASM memory if the module supports it.
	if len(args) > 0 {
		if passErr := f.passArgs(ctx, mod, args); passErr != nil {
			return nil, fmt.Errorf("pass arguments: %w", passErr)
		}
	}

	// Call the execute function
	execFn := mod.ExportedFunction("execute")
	if execFn == nil {
		return nil, fmt.Errorf("WASM module does not export 'execute' function")
	}

	results, err := execFn.Call(ctx)
	if err != nil {
		return nil, fmt.Errorf("WASM execute: %w", err)
	}

	// Try the rich protocol (JSON via memory) first
	if richResult, readErr := f.readResult(ctx, mod); readErr == nil && richResult != nil {
		return richResult, nil
	}

	// Fall back to raw return value from execute
	if len(results) > 0 {
		return results[0], nil
	}

	return nil, nil
}

// passArgs writes JSON-encoded arguments into WASM linear memory.
func (f *WASMFunction) passArgs(ctx context.Context, mod api.Module, args []interface{}) error {
	jsonData, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("marshal args: %w", err)
	}

	// Allocate memory in WASM for the args
	allocFn := mod.ExportedFunction("alloc")
	if allocFn == nil {
		return fmt.Errorf("no 'alloc' export")
	}

	results, err := allocFn.Call(ctx, uint64(len(jsonData)))
	if err != nil {
		return fmt.Errorf("alloc: %w", err)
	}
	ptr := uint32(results[0])

	// Write data to WASM memory
	mem := mod.Memory()
	if mem == nil {
		return fmt.Errorf("no memory export")
	}
	if !mem.Write(ptr, jsonData) {
		return fmt.Errorf("failed to write to WASM memory at %d", ptr)
	}

	// Call execute_args(ptr, len) if it exists
	argsFn := mod.ExportedFunction("execute_args")
	if argsFn != nil {
		_, err = argsFn.Call(ctx, uint64(ptr), uint64(len(jsonData)))
		if err != nil {
			return fmt.Errorf("execute_args: %w", err)
		}
	}

	return nil
}

// readResult reads the result from WASM memory using the rich protocol.
func (f *WASMFunction) readResult(ctx context.Context, mod api.Module) (interface{}, error) {
	resultLenFn := mod.ExportedFunction("result_len")
	resultCopyFn := mod.ExportedFunction("result_copy")

	// If the rich protocol isn't available, return nil (simple mode)
	if resultLenFn == nil || resultCopyFn == nil {
		return nil, nil
	}

	// Get result length
	lenResults, err := resultLenFn.Call(ctx)
	if err != nil {
		return nil, fmt.Errorf("result_len: %w", err)
	}
	length := uint32(lenResults[0])

	if length == 0 {
		return nil, nil
	}

	// Allocate memory for the result
	allocFn := mod.ExportedFunction("alloc")
	if allocFn == nil {
		return nil, fmt.Errorf("no 'alloc' export for result")
	}
	allocResults, err := allocFn.Call(ctx, uint64(length))
	if err != nil {
		return nil, fmt.Errorf("alloc for result: %w", err)
	}
	resultPtr := uint32(allocResults[0])

	// Copy result to memory
	_, err = resultCopyFn.Call(ctx, uint64(resultPtr))
	if err != nil {
		return nil, fmt.Errorf("result_copy: %w", err)
	}

	// Read from memory
	mem := mod.Memory()
	if mem == nil {
		return nil, fmt.Errorf("no memory export")
	}
	data, ok := mem.Read(resultPtr, length)
	if !ok {
		return nil, fmt.Errorf("failed to read result from WASM memory at %d", resultPtr)
	}

	// Parse JSON result
	var result interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("unmarshal result: %w", err)
	}

	return result, nil
}
