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
	ErrInvokeForbiddenEntity  = errors.New(contracts.CodeForbiddenEntity)
	ErrInvokeInvalidRequest   = errors.New(contracts.CodeInvalidRequest)
	ErrInvokePayloadTooLarge  = errors.New(contracts.CodePayloadTooLarge)
	ErrInvokeExecutionFailed  = errors.New(contracts.CodeExecutionFailed)
	ErrInvokeExecutionTimeout = errors.New(contracts.CodeExecutionTimeout)
	ErrInvokeCancelled        = errors.New(contracts.CodeCancelled)
	ErrInvokeOverloaded       = errors.New(contracts.CodeOverloaded)
)

const maxConcurrentInvocations = 4

// Invocation is one host-validated runtime.invoke request. The WASM module only
// ever sees the composed input built by the host, never the raw peer payload.
type Invocation struct {
	Command  string
	Entities []string
	Input    json.RawMessage
}

// DecodeInvocation strictly decodes the versioned invoke-request document.
// The returned code is empty on success and otherwise one of the error codes
// in the versioned invoke contract; bodies that fail schema shape are invalid.
func DecodeInvocation(raw []byte) (Invocation, string) {
	var request struct {
		Command  string          `json:"command"`
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
	if !config.ValidIdentifier(request.Command) ||
		len(request.Input) == 0 || !json.Valid(request.Input) || len(request.Entities) > 128 {
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
		Command: request.Command, Entities: append([]string(nil), request.Entities...), Input: append([]byte(nil), request.Input...),
	}, ""
}

type Invoker struct {
	store       *config.ConfigurationStore
	executor    wasmExecutor
	hostCaller  PluginCaller
	drainMu     sync.Mutex
	accepting   bool
	inFlight    int
	maxInFlight int
	drained     chan struct{}
	drainClosed bool
}

type wasmExecutor interface {
	ExecuteWithHostExport(context.Context, []byte, string, []byte, wasm.HostCall) ([]byte, error)
}

// PluginCaller is the only outbound capability exposed to WASM host calls. Its
// implementation must use Runtime's authenticated generic pluginprotocol
// session and must never retry side-effecting calls.
type PluginCaller interface {
	Call(context.Context, string, string, json.RawMessage) (json.RawMessage, error)
}

func NewInvoker(store *config.ConfigurationStore) *Invoker {
	return &Invoker{store: store, executor: wasm.Executor{}, accepting: true, maxInFlight: maxConcurrentInvocations, drained: make(chan struct{})}
}

func NewInvokerWithHostCaller(store *config.ConfigurationStore, caller PluginCaller) *Invoker {
	invoker := NewInvoker(store)
	invoker.hostCaller = caller
	return invoker
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

func (invoker *Invoker) admit() error {
	invoker.drainMu.Lock()
	defer invoker.drainMu.Unlock()
	if !invoker.accepting {
		return ErrInvokeNotReady
	}
	if invoker.inFlight >= invoker.maxInFlight {
		return ErrInvokeOverloaded
	}
	invoker.inFlight++
	return nil
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
// the command entity allowlist and composes the bounded host-normalized WASM
// input, so a module can never observe an undeclared entity.
func (invoker *Invoker) Invoke(ctx context.Context, request Invocation) (json.RawMessage, error) {
	if invoker == nil {
		return nil, ErrInvokeNotReady
	}
	if err := invoker.admit(); err != nil {
		return nil, err
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
	var output []byte
	found, err := invoker.store.WithCommand(request.Command, func(view config.CommandView) error {
		command := &view.Command
		for _, entity := range request.Entities {
			if !containsString(command.Entities, entity) {
				return ErrInvokeForbiddenEntity
			}
		}
		composed, err := composeInvocation(command, request)
		if err != nil {
			return err
		}
		result, err := invoker.executor.ExecuteWithHostExport(ctx, view.Module, command.Export, composed, func(callCtx context.Context, plugin, method string, payload json.RawMessage) (json.RawMessage, error) {
			if !commandAllowsCall(command, plugin, method) || invoker.hostCaller == nil {
				return nil, ErrInvokeForbiddenEntity
			}
			if plugin == "domain" && !commandAllowsDomainPayload(command, method, payload) {
				return nil, ErrInvokeForbiddenEntity
			}
			return invoker.hostCaller.Call(callCtx, plugin, method, payload)
		})
		if err != nil {
			return err
		}
		output = result
		return nil
	})
	if !found && err == nil {
		if !invoker.store.Ready() {
			return nil, ErrInvokeNotReady
		}
		return nil, ErrInvokeUnknownCommand
	}
	if err != nil {
		switch {
		case errors.Is(err, ErrInvokeForbiddenEntity):
			return nil, ErrInvokeForbiddenEntity
		case errors.Is(err, ErrInvokeInvalidRequest):
			return nil, ErrInvokeInvalidRequest
		case errors.Is(err, ErrInvokePayloadTooLarge):
			return nil, ErrInvokePayloadTooLarge
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

func commandAllowsCall(command *models.Command, plugin, method string) bool {
	for _, allowed := range command.Calls {
		if allowed.Plugin == plugin && allowed.Method == method {
			return true
		}
	}
	return false
}

func commandAllowsDomainPayload(command *models.Command, method string, payload json.RawMessage) bool {
	if method == "domain.query" {
		var request struct {
			SQL string `json:"sql"`
		}
		if json.Unmarshal(payload, &request) != nil {
			return false
		}
		return queryEntitiesAllowed(request.SQL, command.Entities)
	}
	var request struct {
		Entity     string `json:"entity"`
		Operations []struct {
			Entity string `json:"entity"`
		} `json:"operations"`
	}
	if json.Unmarshal(payload, &request) != nil {
		return false
	}
	if method == "domain.transaction" {
		if len(request.Operations) == 0 {
			return false
		}
		for _, operation := range request.Operations {
			if operation.Entity == "" { // read-only operations are still entity-scoped.
				return false
			}
			if !containsString(command.Entities, operation.Entity) {
				return false
			}
		}
		return true
	}
	if method == "domain.diagnostics" {
		return true
	}
	return request.Entity != "" && containsString(command.Entities, request.Entity)
}

// queryEntitiesAllowed extracts FROM/JOIN sources while ignoring SQL string
// literals. Domain's query grammar rejects comments, subqueries, quoted names,
// and set operations; this host-side check fails closed if the statement has
// no concrete source or a source outside the command allowlist.
func queryEntitiesAllowed(sql string, allowed []string) bool {
	tables := make([]string, 0, 4)
	words := sqlWords(sql)
	for index, word := range words {
		if equalSQLWord(word, "FROM") || equalSQLWord(word, "JOIN") {
			if index+1 >= len(words) || !validSQLIdentifier(words[index+1]) {
				return false
			}
			tables = append(tables, words[index+1])
		}
	}
	if len(tables) == 0 {
		return false
	}
	for _, table := range tables {
		if !containsString(allowed, table) {
			return false
		}
	}
	return true
}

func sqlWords(sql string) []string {
	words := make([]string, 0, 16)
	for index := 0; index < len(sql); {
		if sql[index] == '\'' {
			index++
			for index < len(sql) {
				if sql[index] == '\'' {
					index++
					if index < len(sql) && sql[index] == '\'' {
						index++
						continue
					}
					break
				}
				index++
			}
			continue
		}
		if isSQLWordStart(sql[index]) {
			start := index
			index++
			for index < len(sql) && isSQLWordPart(sql[index]) {
				index++
			}
			words = append(words, sql[start:index])
			continue
		}
		index++
	}
	return words
}

func equalSQLWord(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		char := left[index]
		if char >= 'a' && char <= 'z' {
			char -= 'a' - 'A'
		}
		if char != right[index] {
			return false
		}
	}
	return true
}

func validSQLIdentifier(value string) bool {
	if value == "" || !isSQLWordStart(value[0]) {
		return false
	}
	for index := 1; index < len(value); index++ {
		if !isSQLWordPart(value[index]) {
			return false
		}
	}
	return true
}

func isSQLWordStart(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func isSQLWordPart(value byte) bool { return isSQLWordStart(value) || value >= '0' && value <= '9' }

// composeInvocation builds and bounds the exact JSON document handed to a WASM
// module. It carries the host-validated command and normalized entity list.
func composeInvocation(command *models.Command, request Invocation) ([]byte, error) {
	document := struct {
		Command  string          `json:"command"`
		Entities []string        `json:"entities"`
		Input    json.RawMessage `json:"input"`
	}{Command: command.Name, Entities: request.Entities, Input: request.Input}
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
