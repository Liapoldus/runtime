package module

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/Liapoldus/runtime/contracts"
	"github.com/Liapoldus/runtime/internal/application/config"
	"github.com/Liapoldus/runtime/internal/domain/models"
	"github.com/Liapoldus/runtime/internal/infrastructure/wasm"
)

var (
	ErrInvokeNotReady         = errors.New(contracts.CodeNotReady)
	ErrInvokeUnknownCommand   = errors.New(contracts.CodeUnknownCommand)
	ErrInvokeForbiddenScope   = errors.New(contracts.CodeForbiddenScope)
	ErrInvokeForbiddenEntity  = errors.New(contracts.CodeForbiddenEntity)
	ErrInvokeInvalidRequest   = errors.New(contracts.CodeInvalidRequest)
	ErrInvokePayloadTooLarge  = errors.New(contracts.CodePayloadTooLarge)
	ErrInvokeExecutionFailed  = errors.New(contracts.CodeExecutionFailed)
	ErrInvokeExecutionTimeout = errors.New(contracts.CodeExecutionTimeout)
	ErrInvokeCancelled        = errors.New(contracts.CodeCancelled)
)

// Invocation is one host-validated runtime.invoke request. The WASM module only
// ever sees the composed input built by the host, never the raw peer payload.
type Invocation struct {
	Command  string
	Scope    string
	Entities []string
	Input    json.RawMessage
}

// DecodeInvocation strictly decodes the versioned invoke-request document.
// The returned code is empty on success and otherwise one of the error codes
// in the versioned invoke contract; bodies that fail schema shape are invalid.
func DecodeInvocation(raw []byte) (Invocation, string) {
	var request struct {
		Command  string          `json:"command"`
		Scope    string          `json:"scope"`
		Entities []string        `json:"entities"`
		Input    json.RawMessage `json:"input"`
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return Invocation{}, contracts.CodeInvalidRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return Invocation{}, contracts.CodeInvalidRequest
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Invocation{}, contracts.CodeInvalidRequest
	}
	if !config.ValidIdentifier(request.Command) || !config.ValidTenantSite(request.Scope) ||
		len(request.Input) == 0 || !json.Valid(request.Input) {
		return Invocation{}, contracts.CodeInvalidRequest
	}
	seen := make(map[string]struct{}, len(request.Entities))
	for _, entity := range request.Entities {
		if !config.ValidIdentifier(entity) {
			return Invocation{}, contracts.CodeInvalidRequest
		}
		if _, duplicate := seen[entity]; duplicate {
			return Invocation{}, contracts.CodeInvalidRequest
		}
		seen[entity] = struct{}{}
	}
	return Invocation{
		Command: request.Command, Scope: request.Scope,
		Entities: append([]string(nil), request.Entities...), Input: append([]byte(nil), request.Input...),
	}, ""
}

type Invoker struct {
	store       *config.ConfigurationStore
	executor    wasm.Executor
	drainMu     sync.Mutex
	accepting   bool
	inFlight    int
	drained     chan struct{}
	drainClosed bool
}

func NewInvoker(store *config.ConfigurationStore) *Invoker {
	return &Invoker{store: store, accepting: true, drained: make(chan struct{})}
}

// Accepting reports whether this process may admit new peer invocations.
func (invoker *Invoker) Accepting() bool {
	if invoker == nil {
		return false
	}
	invoker.drainMu.Lock()
	defer invoker.drainMu.Unlock()
	return invoker.accepting
}

// BeginDrain fences new invocations while allowing work already admitted to
// finish. It is safe to call repeatedly during process shutdown.
func (invoker *Invoker) BeginDrain() {
	if invoker == nil {
		return
	}
	invoker.drainMu.Lock()
	defer invoker.drainMu.Unlock()
	invoker.accepting = false
	invoker.closeDrainedWhenIdle()
}

// WaitForDrain waits for every invocation admitted before BeginDrain to finish.
// The caller bounds this wait by the Plugin SDK shutdown grace.
func (invoker *Invoker) WaitForDrain(ctx context.Context) error {
	if invoker == nil || ctx == nil {
		return ErrInvokeExecutionFailed
	}
	invoker.BeginDrain()
	select {
	case <-invoker.drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (invoker *Invoker) admit() bool {
	invoker.drainMu.Lock()
	defer invoker.drainMu.Unlock()
	if !invoker.accepting {
		return false
	}
	invoker.inFlight++
	return true
}

func (invoker *Invoker) complete() {
	invoker.drainMu.Lock()
	defer invoker.drainMu.Unlock()
	invoker.inFlight--
	invoker.closeDrainedWhenIdle()
}

func (invoker *Invoker) closeDrainedWhenIdle() {
	if !invoker.accepting && invoker.inFlight == 0 && !invoker.drainClosed {
		close(invoker.drained)
		invoker.drainClosed = true
	}
}

// Invoke runs one accepted invocation against the active generation. It applies
// the command scope/entity allowlist and composes the bounded host-normalized
// WASM input, so a module can never observe an undeclared scope or entity.
func (invoker *Invoker) Invoke(ctx context.Context, request Invocation) (json.RawMessage, error) {
	if invoker == nil || !invoker.admit() {
		return nil, ErrInvokeNotReady
	}
	defer invoker.complete()
	if ctx == nil {
		return nil, ErrInvokeExecutionFailed
	}
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, ErrInvokeCancelled
		}
		return nil, ErrInvokeExecutionTimeout
	}
	snapshot, ok := invoker.store.Snapshot()
	if !ok {
		return nil, ErrInvokeNotReady
	}
	var command *models.Command
	for index := range snapshot.Settings.Commands {
		if snapshot.Settings.Commands[index].Name == request.Command {
			command = &snapshot.Settings.Commands[index]
			break
		}
	}
	if command == nil {
		return nil, ErrInvokeUnknownCommand
	}
	if !containsString(command.TenantSites, request.Scope) {
		return nil, ErrInvokeForbiddenScope
	}
	for _, entity := range request.Entities {
		if !containsString(command.Entities, entity) {
			return nil, ErrInvokeForbiddenEntity
		}
	}
	composed, err := composeInvocation(snapshot.Settings, request)
	if err != nil {
		return nil, err
	}
	output, err := invoker.executor.Execute(ctx, snapshot.Module, composed)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return nil, ErrInvokeCancelled
		case errors.Is(err, wasm.ErrExecutionTimeout):
			return nil, ErrInvokeExecutionTimeout
		case errors.Is(err, wasm.ErrTooLarge):
			return nil, ErrInvokePayloadTooLarge
		default:
			return nil, ErrInvokeExecutionFailed
		}
	}
	return json.RawMessage(output), nil
}

// composeInvocation builds and bounds the exact JSON document handed to a WASM
// module. It carries the host-validated scope and a normalized entity list.
func composeInvocation(configuration models.Configuration, request Invocation) ([]byte, error) {
	document := struct {
		Command  string          `json:"command"`
		Scope    string          `json:"scope"`
		Entities []string        `json:"entities"`
		Input    json.RawMessage `json:"input"`
	}{Command: request.Command, Scope: request.Scope, Entities: request.Entities, Input: request.Input}
	if document.Entities == nil {
		document.Entities = []string{}
	}
	composed, err := json.Marshal(document)
	if err != nil {
		return nil, ErrInvokeExecutionFailed
	}
	abi := contracts.WasmABIDefinition()
	if len(composed) > abi.Limits.MaxInputBytes {
		return nil, ErrInvokePayloadTooLarge
	}
	return composed, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
