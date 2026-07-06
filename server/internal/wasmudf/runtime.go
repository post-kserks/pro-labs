package wasmudf

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

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
)

// allowedExports is the whitelist of function names that WASM UDF modules may export.
var allowedExports = map[string]bool{
	"alloc":         true,
	"execute":       true,
	"execute_args":  true,
	"result_len":    true,
	"result_copy":   true,
}

// Runtime manages WASM module compilation and execution.
type Runtime struct {
	runtime        wazero.Runtime
	defaultTimeout time.Duration
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

	return &Runtime{runtime: r, defaultTimeout: timeout}, nil
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
