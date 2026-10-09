package wasm

import (
	"context"
	"encoding/json"
	"errors"
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
// filesystem, network or another plugin through this adapter.
type Executor struct{}

// ValidateModule compiles one bounded WASM module and verifies the exact
// product ABI without instantiating it or executing its start function.
func ValidateModule(ctx context.Context, moduleBytes []byte) (resultErr error) {
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
	return executionError(ctx)
}

func (Executor) Execute(ctx context.Context, moduleBytes, input []byte) (resultBytes []byte, resultErr error) {
	if err := executionError(ctx); err != nil {
		return nil, err
	}
	abi := contracts.WasmABIDefinition()
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
	instance, err := runtime.InstantiateModule(ctx, compiled, wazero.NewModuleConfig())
	if err != nil {
		if interrupted := executionError(ctx); interrupted != nil {
			return nil, interrupted
		}
		return nil, ErrInvalidModule
	}
	defer func() { resultErr = errors.Join(resultErr, instance.Close(ctx)) }()
	allocate := instance.ExportedFunction(abi.Exports.Allocate)
	invoke := instance.ExportedFunction(abi.Exports.Invoke)
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

// ValidateCompiledModule verifies the module shape declared by the Runtime ABI
// without instantiating or executing untrusted start functions. Runtime modules
// may not import host functions or memory, and their exported functions must
// have the exact signatures required by the contract.
func ValidateCompiledModule(compiled wazero.CompiledModule) error {
	if compiled == nil {
		return ErrInvalidABI
	}
	abi := contracts.WasmABIDefinition()
	if len(compiled.ImportedFunctions()) != 0 || len(compiled.ImportedMemories()) != 0 {
		return ErrInvalidABI
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
