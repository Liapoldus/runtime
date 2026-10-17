package wasm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/Liapoldus/runtime/contracts"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

var (
	ErrInvalidModule    = errors.New(contracts.CodeInvalidModule)
	ErrInvalidABI       = errors.New(contracts.CodeInvalidABI)
	ErrInvalidJSON      = errors.New(contracts.CodeInvalidJSON)
	ErrTooLarge         = errors.New(contracts.CodePayloadTooLarge)
	ErrExecutionFailed  = errors.New(contracts.CodeExecutionFailed)
	ErrExecutionTimeout = errors.New(contracts.CodeExecutionTimeout)
)

// Executor runs one versioned JSON ABI invocation in a new isolated WASM
// instance. No WASI or host module is registered, so a module cannot access
// filesystem, network or another service through this adapter.
type Executor struct{}

type HostCall func(context.Context, string, string, json.RawMessage) (json.RawMessage, error)

type hostRequest struct {
	Service string          `json:"service"`
	Method  string          `json:"method"`
	Payload json.RawMessage `json:"payload"`
}

type hostResponse struct {
	OK      bool            `json:"ok"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// ValidateModule compiles one bounded WASM module and verifies the exact
// product ABI without instantiating it or executing its start function.
func ValidateModule(ctx context.Context, moduleBytes []byte) (resultErr error) {
	return validateModule(ctx, moduleBytes, nil)
}

// ValidateModuleExports also verifies every configured route export has the
// same function signature as the ABI invocation entrypoint.
func ValidateModuleExports(ctx context.Context, moduleBytes []byte, exports []string) error {
	if len(exports) == 0 {
		return ErrInvalidABI
	}
	return validateModule(ctx, moduleBytes, exports)
}

func validateModule(ctx context.Context, moduleBytes []byte, routeExports []string) (resultErr error) {
	if ctx == nil {
		return ErrInvalidModule
	}
	if err := executionError(ctx); err != nil {
		return err
	}
	abi := contracts.WasmABIDefinition()
	if len(moduleBytes) == 0 || len(moduleBytes) > abi.Limits.MaxModuleBytes {
		return ErrTooLarge
	}
	runtime := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfigInterpreter().WithMemoryLimitPages(abi.Limits.MaxMemoryPages).WithCloseOnContextDone(true))
	defer func() { resultErr = errors.Join(resultErr, runtime.Close(ctx)) }()
	compiled, err := runtime.CompileModule(ctx, moduleBytes)
	if err != nil {
		if interrupted := executionError(ctx); interrupted != nil {
			return interrupted
		}
		return ErrInvalidModule
	}
	defer func() { resultErr = errors.Join(resultErr, compiled.Close(ctx)) }()
	if err := rejectModuleImports(moduleBytes); err != nil {
		return err
	}
	if err := ValidateCompiledModule(compiled); err != nil {
		return err
	}
	functions := compiled.ExportedFunctions()
	for _, name := range routeExports {
		function, ok := functions[name]
		if !ok || !hasSignature(function, abi.Signatures.Invoke) {
			return ErrInvalidABI
		}
	}
	return executionError(ctx)
}

// ExecuteWithHostExport invokes one configured ABI-compatible function export.
func (Executor) ExecuteWithHostExport(ctx context.Context, moduleBytes []byte, exportName string, input []byte, hostCall HostCall) (resultBytes []byte, resultErr error) {
	if err := executionError(ctx); err != nil {
		return nil, err
	}
	abi := contracts.WasmABIDefinition()
	if !validExportName(exportName) {
		return nil, ErrInvalidABI
	}
	if len(input) > abi.Limits.MaxInputBytes || len(moduleBytes) > abi.Limits.MaxModuleBytes {
		return nil, ErrTooLarge
	}
	if !json.Valid(input) {
		return nil, ErrInvalidJSON
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(abi.Limits.MaxDurationMillis)*time.Millisecond)
	defer cancel()
	runtime := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfigInterpreter().WithMemoryLimitPages(abi.Limits.MaxMemoryPages).WithCloseOnContextDone(true))
	defer func() { resultErr = errors.Join(resultErr, runtime.Close(ctx)) }()
	compiled, err := runtime.CompileModule(ctx, moduleBytes)
	if err != nil {
		if interrupted := executionError(ctx); interrupted != nil {
			return nil, interrupted
		}
		return nil, ErrInvalidModule
	}
	defer func() { resultErr = errors.Join(resultErr, compiled.Close(ctx)) }()
	if err := rejectModuleImports(moduleBytes); err != nil {
		return nil, err
	}
	if err := ValidateCompiledModule(compiled); err != nil {
		return nil, err
	}
	configuredExport, exists := compiled.ExportedFunctions()[exportName]
	if !exists || !hasSignature(configuredExport, abi.Signatures.Invoke) {
		return nil, ErrInvalidABI
	}
	if len(compiled.ImportedFunctions()) > 0 {
		if hostCall == nil {
			return nil, ErrExecutionFailed
		}
		callCount := 0
		abi := contracts.WasmABIDefinition()
		_, err := runtime.NewHostModuleBuilder(abi.HostModules[0]).
			NewFunctionBuilder().WithFunc(func(callCtx context.Context, guest api.Module, pointer, length uint32) uint64 {
			if callCount >= abi.Limits.MaxHostCalls || int(length) > abi.Limits.MaxHostCallBytes {
				return hostFailure(guest, callCtx, abi.Exports.Allocate, abi.Limits.MaxHostCallBytes)
			}
			callCount++
			memory := guest.ExportedMemory(abi.Exports.Memory)
			inputBytes, ok := memory.Read(pointer, length)
			if !ok || len(inputBytes) > abi.Limits.MaxHostCallBytes {
				return hostFailure(guest, callCtx, abi.Exports.Allocate, abi.Limits.MaxHostCallBytes)
			}
			var request hostRequest
			decoder := json.NewDecoder(bytes.NewReader(append([]byte(nil), inputBytes...)))
			decoder.DisallowUnknownFields()
			decodeErr := decoder.Decode(&request)
			var trailing any
			if decodeErr != nil || decoder.Decode(&trailing) != io.EOF || request.Service == "" || request.Method == "" || len(request.Payload) == 0 || !json.Valid(request.Payload) {
				return hostFailure(guest, callCtx, abi.Exports.Allocate, abi.Limits.MaxHostCallBytes)
			}
			response := hostResponse{}
			payload, callErr := hostCall(callCtx, request.Service, request.Method, append(json.RawMessage(nil), request.Payload...))
			if callErr != nil {
				response.Error = "call_failed"
			} else if len(payload) > abi.Limits.MaxHostCallBytes || !json.Valid(payload) {
				response.Error = "invalid_response"
			} else {
				response.OK = true
				response.Payload = append(json.RawMessage(nil), payload...)
			}
			encoded, marshalErr := json.Marshal(response)
			if marshalErr != nil || len(encoded) > abi.Limits.MaxHostCallBytes {
				return hostFailure(guest, callCtx, abi.Exports.Allocate, abi.Limits.MaxHostCallBytes)
			}
			return writeGuestBytes(callCtx, guest, abi.Exports.Allocate, encoded)
		}).Export("call").Instantiate(ctx)
		if err != nil {
			if interrupted := executionError(ctx); interrupted != nil {
				return nil, interrupted
			}
			return nil, ErrInvalidABI
		}
	}
	instance, err := runtime.InstantiateModule(ctx, compiled, wazero.NewModuleConfig())
	if err != nil {
		if interrupted := executionError(ctx); interrupted != nil {
			return nil, interrupted
		}
		return nil, ErrInvalidModule
	}
	defer func() { resultErr = errors.Join(resultErr, instance.Close(ctx)) }()
	allocate := instance.ExportedFunction(abi.Exports.Allocate)
	invoke := instance.ExportedFunction(exportName)
	memory := instance.ExportedMemory(abi.Exports.Memory)
	allocated, err := allocate.Call(ctx, uint64(len(input)))
	if err != nil {
		if interrupted := executionError(ctx); interrupted != nil {
			return nil, interrupted
		}
		return nil, ErrExecutionFailed
	}
	if len(allocated) != 1 {
		return nil, ErrInvalidABI
	}
	pointer := uint32(allocated[0] & 0xffffffff)
	if !memory.Write(pointer, input) {
		return nil, ErrInvalidABI
	}
	result, err := invoke.Call(ctx, uint64(pointer), uint64(len(input)))
	if err != nil {
		if interrupted := executionError(ctx); interrupted != nil {
			return nil, interrupted
		}
		return nil, ErrExecutionFailed
	}
	if len(result) != 1 {
		return nil, ErrInvalidABI
	}
	resultPointer := uint32(result[0] & 0xffffffff)
	resultLength := uint32(result[0] >> 32)
	if int64(resultLength) > int64(abi.Limits.MaxOutputBytes) {
		return nil, ErrTooLarge
	}
	output, ok := memory.Read(resultPointer, resultLength)
	if !ok {
		return nil, ErrInvalidABI
	}
	if !json.Valid(output) {
		return nil, ErrInvalidJSON
	}
	if err := executionError(ctx); err != nil {
		return nil, err
	}
	return append([]byte(nil), output...), nil
}

func validExportName(value string) bool {
	if value == "" || len(value) > 64 || !asciiAlpha(value[0]) {
		return false
	}
	for index := 1; index < len(value); index++ {
		character := value[index]
		if asciiAlpha(character) || character >= '0' && character <= '9' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func asciiAlpha(character byte) bool {
	return character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z'
}

// ValidateCompiledModule verifies the module shape declared by the Runtime ABI
// without instantiating or executing untrusted start functions. Runtime modules
// may not import host functions or memory, and their exported functions must
// have the exact signatures required by the contract.
func ValidateCompiledModule(compiled wazero.CompiledModule) error {
	if compiled == nil {
		return ErrInvalidABI
	}
	abi := contracts.WasmABIDefinition()
	if len(compiled.ImportedFunctions()) > 1 || len(compiled.ImportedMemories()) != 0 {
		return ErrInvalidABI
	}
	if imported := compiled.ImportedFunctions(); len(imported) == 1 {
		moduleName, name, ok := imported[0].Import()
		abi := contracts.WasmABIDefinition()
		if !ok || moduleName != abi.HostModules[0] || name != "call" || !hasSignature(imported[0], contracts.WasmFunctionSignature{Parameters: []string{"i32", "i32"}, Results: []string{"i64"}}) {
			return ErrInvalidABI
		}
	}
	functions := compiled.ExportedFunctions()
	allocate, ok := functions[abi.Exports.Allocate]
	if !ok || !hasSignature(allocate, abi.Signatures.Allocate) {
		return ErrInvalidABI
	}
	invoke, ok := functions[abi.Exports.Invoke]
	if !ok || !hasSignature(invoke, abi.Signatures.Invoke) {
		return ErrInvalidABI
	}
	memory, ok := compiled.ExportedMemories()[abi.Exports.Memory]
	if !ok {
		return ErrInvalidABI
	}
	if _, _, imported := memory.Import(); imported || memory.Min() > abi.Limits.MaxMemoryPages {
		return ErrInvalidABI
	}
	return nil
}

func writeGuestBytes(ctx context.Context, guest api.Module, allocateName string, data []byte) uint64 {
	if uint64(len(data)) > uint64(^uint32(0)) {
		return 0
	}
	allocated, err := guest.ExportedFunction(allocateName).Call(ctx, uint64(len(data)))
	if err != nil || len(allocated) != 1 || allocated[0] > uint64(^uint32(0)) {
		return 0
	}
	pointer := uint32(allocated[0] & 0xffffffff)
	if !guest.ExportedMemory("memory").Write(pointer, data) {
		return 0
	}
	return allocated[0] | uint64(len(data))<<32
}

func hostFailure(guest api.Module, ctx context.Context, allocateName string, maxBytes int) uint64 {
	data := []byte(`{"ok":false,"error":"host_call_rejected"}`)
	if len(data) > maxBytes {
		return 0
	}
	return writeGuestBytes(ctx, guest, allocateName, data)
}

func hasSignature(function api.FunctionDefinition, signature contracts.WasmFunctionSignature) bool {
	if function == nil || len(function.ParamTypes()) != len(signature.Parameters) || len(function.ResultTypes()) != len(signature.Results) {
		return false
	}
	for index, valueType := range signature.Parameters {
		if !matchesValueType(function.ParamTypes()[index], valueType) {
			return false
		}
	}
	for index, valueType := range signature.Results {
		if !matchesValueType(function.ResultTypes()[index], valueType) {
			return false
		}
	}
	return true
}

func matchesValueType(actual api.ValueType, expected string) bool {
	switch expected {
	case "i32":
		return actual == api.ValueTypeI32
	case "i64":
		return actual == api.ValueTypeI64
	case "f32":
		return actual == api.ValueTypeF32
	case "f64":
		return actual == api.ValueTypeF64
	default:
		return false
	}
}

func executionError(ctx context.Context) error {
	switch ctx.Err() {
	case nil:
		return nil
	case context.Canceled:
		return context.Canceled
	case context.DeadlineExceeded:
		return ErrExecutionTimeout
	default:
		return ErrExecutionFailed
	}
}
