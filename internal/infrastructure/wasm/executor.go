package wasm

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Liapoldus/runtime/contracts"
	"github.com/tetratelabs/wazero"
)

var (
	ErrInvalidModule    = errors.New("invalid_module")
	ErrInvalidABI       = errors.New("invalid_abi")
	ErrInvalidJSON      = errors.New("invalid_json")
	ErrTooLarge         = errors.New("payload_too_large")
	ErrExecutionFailed  = errors.New("execution_failed")
	ErrExecutionTimeout = errors.New("execution_timeout")
)

// Executor runs one versioned JSON ABI invocation in a new isolated WASM
// instance. No WASI or host module is registered, so a module cannot access
// filesystem, network or another plugin through this adapter.
type Executor struct{}

func (Executor) Execute(ctx context.Context, moduleBytes, input []byte) ([]byte, error) {
	abi, err := contracts.LoadWasmABI()
	if err != nil {
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
	defer runtime.Close(ctx)
	compiled, err := runtime.CompileModule(ctx, moduleBytes)
	if err != nil {
		return nil, ErrInvalidModule
	}
	defer compiled.Close(ctx)
	instance, err := runtime.InstantiateModule(ctx, compiled, wazero.NewModuleConfig())
	if err != nil {
		return nil, ErrInvalidModule
	}
	defer instance.Close(ctx)
	allocate := instance.ExportedFunction(abi.Exports.Allocate)
	invoke := instance.ExportedFunction(abi.Exports.Invoke)
	memory := instance.ExportedMemory(abi.Exports.Memory)
	if allocate == nil || invoke == nil || memory == nil {
		return nil, ErrInvalidABI
	}
	allocated, err := allocate.Call(ctx, uint64(len(input)))
	if err != nil || len(allocated) != 1 {
		return nil, executionError(ctx)
	}
	pointer := uint32(allocated[0])
	if !memory.Write(pointer, input) {
		return nil, ErrInvalidABI
	}
	result, err := invoke.Call(ctx, uint64(pointer), uint64(len(input)))
	if err != nil || len(result) != 1 {
		return nil, executionError(ctx)
	}
	resultPointer := uint32(result[0])
	resultLength := uint32(result[0] >> 32)
	if resultLength > uint32(abi.Limits.MaxOutputBytes) {
		return nil, ErrTooLarge
	}
	output, ok := memory.Read(resultPointer, resultLength)
	if !ok {
		return nil, ErrInvalidABI
	}
	if !json.Valid(output) {
		return nil, ErrInvalidJSON
	}
	return append([]byte(nil), output...), nil
}

func executionError(ctx context.Context) error {
	if ctx.Err() != nil {
		return ErrExecutionTimeout
	}
	return ErrExecutionFailed
}
