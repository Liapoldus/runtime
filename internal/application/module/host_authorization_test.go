package module

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/Liapoldus/runtime/internal/application/config"
	"github.com/Liapoldus/runtime/internal/domain/models"
	"github.com/Liapoldus/runtime/internal/infrastructure/wasm"
)

func TestInvokeAllocationDoesNotCopyUnselectedArtifacts(t *testing.T) {
	module, err := hex.DecodeString("0061736d01000000010c0260017f017f60027f7f017e03030200010503010001071b03066d656d6f7279020005616c6c6f63000006696e766f6b6500010a1302040041000b0c002001ad4220862000ad840b")
	if err != nil {
		t.Fatal(err)
	}
	unused := bytes.Repeat([]byte{'x'}, 16<<20)
	digest := func(data []byte) string {
		sum := sha256.Sum256(data)
		return hex.EncodeToString(sum[:])
	}
	settings := models.Configuration{
		SchemaVersion: "2",
		Modules: []models.Module{
			{ID: "app", SHA256: digest(module)},
			{ID: "unused", SHA256: digest(unused)},
		},
		Commands: []models.Command{{Name: "submit", ModuleID: "app", Export: "invoke", Entities: []string{"Entry"}}},
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	store := &config.ConfigurationStore{}
	if err := store.Activate(context.Background(), "generation-1", "2", digest(raw), raw, map[string][]byte{"app": module, "unused": unused}); err != nil {
		t.Fatal(err)
	}

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	output, err := NewInvoker(store).Invoke(context.Background(), Invocation{Command: "submit", Input: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	allocated := after.TotalAlloc - before.TotalAlloc
	if len(output) == 0 || allocated > 4<<20 {
		t.Fatalf("invocation allocated %d bytes with an unused 16 MiB artifact; output=%s", allocated, output)
	}
	t.Logf("invocation allocated %d bytes with one unrelated 16 MiB artifact", allocated)
}

func TestDomainHostCallsRespectCommandEntityAllowlist(t *testing.T) {
	command := &models.Command{Entities: []string{"Entry", "Owner"}}
	tests := []struct {
		name    string
		method  string
		payload string
		want    bool
	}{
		{"get allowed", "domain.get", `{"entity":"Entry","id":"x"}`, true},
		{"get denied", "domain.get", `{"entity":"Secret","id":"x"}`, false},
		{"transaction allowed", "domain.transaction", `{"operations":[{"kind":"create","entity":"Entry","object":{}}]}`, true},
		{"transaction denied", "domain.transaction", `{"operations":[{"kind":"get","entity":"Entry"},{"kind":"delete","entity":"Secret"}]}`, false},
		{"query allowed", "domain.query", `{"sql":"SELECT e.id FROM Entry e JOIN Owner o ON e.owner = o.id"}`, true},
		{"query denied join", "domain.query", `{"sql":"SELECT e.id FROM Entry e JOIN Secret s ON e.owner = s.id"}`, false},
		{"query literal keyword ignored", "domain.query", `{"sql":"SELECT 'FROM Secret' FROM Entry"}`, true},
		{"query no source denied", "domain.query", `{"sql":"SELECT 1"}`, false},
		{"diagnostics", "domain.diagnostics", `{}`, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := commandAllowsDomainPayload(command, test.method, json.RawMessage(test.payload)); got != test.want {
				t.Fatalf("allowed=%v, want %v", got, test.want)
			}
		})
	}
}

func TestInvokerRoutesAllowlistedNonDomainPluginCall(t *testing.T) {
	module, err := hex.DecodeString("0061736d01000000010c0260017f017f60027f7f017e03030200010503010001071b03066d656d6f7279020005616c6c6f63000006696e766f6b6500010a1302040041000b0c002001ad4220862000ad840b")
	if err != nil {
		t.Fatal(err)
	}
	digest := func(data []byte) string {
		sum := sha256.Sum256(data)
		return hex.EncodeToString(sum[:])
	}
	settings := models.Configuration{
		SchemaVersion: "2",
		Modules:       []models.Module{{ID: "app", SHA256: digest(module)}},
		Commands: []models.Command{{
			Name: "report", ModuleID: "app", Export: "invoke", Entities: []string{"Entry"},
			Calls: []models.PluginMethod{{Plugin: "metrics", Method: "metrics.aggregate"}},
		}},
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	store := &config.ConfigurationStore{}
	if err := store.Activate(context.Background(), "generation-1", "2", digest(raw), raw, map[string][]byte{"app": module}); err != nil {
		t.Fatal(err)
	}
	caller := &recordingPluginCaller{response: json.RawMessage(`{"count":7}`)}
	invoker := NewInvokerWithHostCaller(store, caller)
	invoker.executor = pluginCallExecutor{}
	output, err := invoker.Invoke(context.Background(), Invocation{Command: "report", Input: json.RawMessage(`{"range":"day"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if caller.plugin != "metrics" || caller.method != "metrics.aggregate" || string(caller.payload) != `{"scope":"daily"}` || string(output) != `{"count":7}` {
		t.Fatalf("non-Domain host call was not routed through the allowlist: caller=%#v output=%s", caller, output)
	}
}

type recordingPluginCaller struct {
	plugin   string
	method   string
	payload  json.RawMessage
	response json.RawMessage
}

func (caller *recordingPluginCaller) Call(_ context.Context, plugin, method string, payload json.RawMessage) (json.RawMessage, error) {
	caller.plugin, caller.method = plugin, method
	caller.payload = append(json.RawMessage(nil), payload...)
	return append(json.RawMessage(nil), caller.response...), nil
}

type pluginCallExecutor struct{}

func (pluginCallExecutor) ExecuteWithHostExport(ctx context.Context, _ []byte, _ string, _ []byte, hostCall wasm.HostCall) ([]byte, error) {
	return hostCall(ctx, "metrics", "metrics.aggregate", json.RawMessage(`{"scope":"daily"}`))
}

func TestInvokerBoundsConcurrentAdmissionsAndDrainAccounting(t *testing.T) {
	invoker := NewInvoker(nil)
	for index := 0; index < 4; index++ {
		if err := invoker.admit(); err != nil {
			t.Fatalf("invocation %d was refused below capacity", index)
		}
	}
	if err := invoker.admit(); !errors.Is(err, ErrInvokeOverloaded) {
		t.Fatal("invocation was admitted above the configured concurrent limit")
	}
	invoker.BeginDrain()
	if invoker.Accepting() {
		t.Fatal("draining invoker still accepts work")
	}
	for range 4 {
		invoker.complete()
	}
	if err := invoker.WaitForDrain(context.Background()); err != nil {
		t.Fatalf("bounded admitted work did not drain: %v", err)
	}
}

func TestInvokerBoundsConcurrentCallsAndDrainsAdmittedInvocations(t *testing.T) {
	module, err := hex.DecodeString("0061736d01000000010c0260017f017f60027f7f017e03030200010503010001071b03066d656d6f7279020005616c6c6f63000006696e766f6b6500010a1302040041000b0c002001ad4220862000ad840b")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(module)
	settings, err := json.Marshal(models.Configuration{
		SchemaVersion: "2",
		Modules:       []models.Module{{ID: "app", SHA256: hex.EncodeToString(digest[:])}},
		Commands:      []models.Command{{Name: "submit", ModuleID: "app", Export: "invoke", Entities: []string{"Entry"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &config.ConfigurationStore{}
	settingsDigest := sha256.Sum256(settings)
	if err := store.Activate(context.Background(), "generation-1", "2", hex.EncodeToString(settingsDigest[:]), settings, map[string][]byte{"app": module}); err != nil {
		t.Fatal(err)
	}

	executor := &blockingInvocationExecutor{
		started: make(chan struct{}, maxConcurrentInvocations),
		release: make(chan struct{}),
	}
	invoker := NewInvoker(store)
	invoker.executor = executor
	invocationResults := make(chan error, maxConcurrentInvocations)
	for range maxConcurrentInvocations {
		go func() {
			_, invokeErr := invoker.Invoke(context.Background(), Invocation{Command: "submit", Input: json.RawMessage(`{}`)})
			invocationResults <- invokeErr
		}()
	}
	for range maxConcurrentInvocations {
		select {
		case <-executor.started:
		case <-time.After(time.Second):
			t.Fatal("invocation did not reach the executor")
		}
	}
	if _, err := invoker.Invoke(context.Background(), Invocation{Command: "submit", Input: json.RawMessage(`{}`)}); !errors.Is(err, ErrInvokeOverloaded) {
		t.Fatalf("fifth invocation error = %v, want overloaded", err)
	}

	invoker.BeginDrain()
	if _, err := invoker.Invoke(context.Background(), Invocation{Command: "submit", Input: json.RawMessage(`{}`)}); !errors.Is(err, ErrInvokeNotReady) {
		t.Fatalf("new invocation during drain error = %v, want not-ready", err)
	}
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelDrain()
	if err := invoker.WaitForDrain(drainCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("drain completed before admitted calls: %v", err)
	}

	close(executor.release)
	for range maxConcurrentInvocations {
		select {
		case err := <-invocationResults:
			if err != nil {
				t.Fatalf("admitted invocation failed: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("admitted invocation did not finish after release")
		}
	}
	if err := invoker.WaitForDrain(context.Background()); err != nil {
		t.Fatalf("drain did not complete after all invocations returned: %v", err)
	}
}

type blockingInvocationExecutor struct {
	started chan struct{}
	release chan struct{}
}

func (executor *blockingInvocationExecutor) ExecuteWithHostExport(ctx context.Context, _ []byte, _ string, _ []byte, _ wasm.HostCall) ([]byte, error) {
	executor.started <- struct{}{}
	select {
	case <-executor.release:
		return []byte(`null`), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type exportRecorder struct {
	export string
	module []byte
}

func (recorder *exportRecorder) ExecuteWithHostExport(_ context.Context, module []byte, export string, _ []byte, _ wasm.HostCall) ([]byte, error) {
	recorder.export = export
	recorder.module = append([]byte(nil), module...)
	return []byte("null"), nil
}

func TestInvokerRoutesToConfiguredModuleAndExport(t *testing.T) {
	modules := map[string][]byte{"forms": []byte("forms-module"), "notify": []byte("notify-module")}
	digest := func(data []byte) string {
		sum := sha256.Sum256(data)
		return hex.EncodeToString(sum[:])
	}
	settings := models.Configuration{
		SchemaVersion: "2",
		Modules: []models.Module{
			{ID: "forms", SHA256: digest(modules["forms"])},
			{ID: "notify", SHA256: digest(modules["notify"])},
		},
		Commands: []models.Command{
			{Name: "submit", ModuleID: "forms", Export: "invoke", Entities: []string{"Entry"}},
			{Name: "send", ModuleID: "notify", Export: "dispatch", Entities: []string{"Entry"}},
		},
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	store := &config.ConfigurationStore{}
	if err := store.Activate(context.Background(), "generation-1", "2", digest(raw), raw, modules); err != nil {
		t.Fatal(err)
	}
	recorder := &exportRecorder{}
	invoker := NewInvoker(store)
	invoker.executor = recorder
	if _, err := invoker.Invoke(context.Background(), Invocation{Command: "send", Input: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if recorder.export != "dispatch" || string(recorder.module) != "notify-module" {
		t.Fatalf("invocation route = module:%q export:%q", recorder.module, recorder.export)
	}
}
