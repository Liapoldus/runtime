package wasm_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Liapoldus/runtime/internal/infrastructure/wasm"
)

func TestExecutorAllowsOnlyBoundedVersionedHostCall(t *testing.T) {
	module, err := hex.DecodeString("0061736d01000000010c0260017f017f60027f7f017e021d01146c6961706f6c6475732e736572766963652e76320463616c6c000103030200010503010001071b03066d656d6f7279020005616c6c6f63000106696e766f6b6500020a0f02040041000b08002000200110000b")
	if err != nil {
		t.Fatal(err)
	}
	if err := wasm.ValidateModule(context.Background(), module); err != nil {
		t.Fatalf("versioned host import rejected: %v", err)
	}
	request := []byte(`{"service":"domain","method":"domain.query","payload":{"sql":"SELECT 1"}}`)
	calls := 0
	output, err := (wasm.Executor{}).ExecuteWithHostExport(context.Background(), module, "invoke", request,
		func(_ context.Context, service, method string, payload json.RawMessage) (json.RawMessage, error) {
			calls++
			if service != "domain" || method != "domain.query" || string(payload) != `{"sql":"SELECT 1"}` {
				t.Fatalf("host call arguments changed: %s %s %s", service, method, payload)
			}
			return json.RawMessage(`{"rows":[]}`), nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || string(output) != `{"ok":true,"payload":{"rows":[]}}` {
		t.Fatalf("unexpected host call result: calls=%d output=%s", calls, output)
	}
	if _, err := (wasm.Executor{}).ExecuteWithHostExport(context.Background(), module, "invoke", request, nil); err == nil {
		t.Fatal("host-importing module executed without an authorized host")
	}
}

func TestExecutorDoesNotRetainMutableGuestStateBetweenInvocations(t *testing.T) {
	module, err := hex.DecodeString("0061736d01000000010c0260017f017f60027f7f017e030302000105030100010606017f0141000b071b03066d656d6f7279020005616c6c6f63000006696e766f6b6500010a2202040041100b1b00230041016a24004100230041306a3a000042004201422086840b")
	if err != nil {
		t.Fatal(err)
	}
	if err := wasm.ValidateModule(context.Background(), module); err != nil {
		t.Fatalf("stateful isolation probe rejected: %v", err)
	}
	executor := wasm.Executor{}
	for invocation := 1; invocation <= 2; invocation++ {
		output, err := executor.ExecuteWithHostExport(context.Background(), module, "invoke", []byte(`{}`), nil)
		if err != nil {
			t.Fatalf("invocation %d failed: %v", invocation, err)
		}
		if string(output) != "1" {
			t.Fatalf("invocation %d observed guest state from an earlier call: output=%s", invocation, output)
		}
	}
}

func TestExecutorHostABIPreservesServiceTargetAndMethod(t *testing.T) {
	module, err := hex.DecodeString("0061736d01000000010c0260017f017f60027f7f017e021d01146c6961706f6c6475732e736572766963652e76320463616c6c000103030200010503010001071b03066d656d6f7279020005616c6c6f63000106696e766f6b6500020a0f02040041000b08002000200110000b")
	if err != nil {
		t.Fatal(err)
	}
	request := []byte(`{"service":"metrics","method":"metrics.aggregate","payload":{"measure":"active-users"}}`)
	calls := 0
	output, err := (wasm.Executor{}).ExecuteWithHostExport(context.Background(), module, "invoke", request,
		func(_ context.Context, service, method string, payload json.RawMessage) (json.RawMessage, error) {
			calls++
			if service != "metrics" || method != "metrics.aggregate" || string(payload) != `{"measure":"active-users"}` {
				t.Fatalf("generic host call was changed: %s %s %s", service, method, payload)
			}
			return json.RawMessage(`{"count":7}`), nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || string(output) != `{"ok":true,"payload":{"count":7}}` {
		t.Fatalf("generic host ABI result: calls=%d output=%s", calls, output)
	}
	legacyRequest := []byte(`{"plugin":"metrics","method":"metrics.aggregate","payload":{"measure":"active-users"}}`)
	legacyCalls := 0
	legacyOutput, err := (wasm.Executor{}).ExecuteWithHostExport(context.Background(), module, "invoke", legacyRequest,
		func(context.Context, string, string, json.RawMessage) (json.RawMessage, error) {
			legacyCalls++
			t.Fatal("legacy plugin target reached the host")
			return nil, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if legacyCalls != 0 || string(legacyOutput) != `{"ok":false,"error":"host_call_rejected"}` {
		t.Fatal("legacy plugin target payload remains accepted")
	}
}

func TestExecutorReturnsHostResponseAfterFiniteGuestLoop(t *testing.T) {
	module := finiteHostCallLoopModule(t, 10)
	if err := wasm.ValidateModule(context.Background(), module); err != nil {
		t.Fatalf("finite host-call module rejected: %v", err)
	}
	calls := 0
	request := []byte(`{"service":"domain","method":"domain.query","payload":{"sql":"SELECT 1"}}`)
	output, err := (wasm.Executor{}).ExecuteWithHostExport(context.Background(), module, "invoke", request,
		func(context.Context, string, string, json.RawMessage) (json.RawMessage, error) {
			calls++
			return json.RawMessage(`{"done":true}`), nil
		})
	if err != nil || calls != 1 || string(output) != `{"ok":true,"payload":{"done":true}}` {
		t.Fatalf("host response was not preserved after guest loop: calls=%d output=%s err=%v", calls, output, err)
	}
}

func finiteHostCallLoopModule(t *testing.T, iterations uint32) []byte {
	t.Helper()
	uleb := func(value int) []byte {
		encoded := make([]byte, 0, 5)
		for value >= 0x80 {
			encoded = append(encoded, byte(value&0x7f)|0x80)
			value >>= 7
		}
		return append(encoded, byte(value&0x7f))
	}
	name := func(value string) []byte { return append(uleb(len(value)), value...) }
	section := func(module []byte, id byte, body []byte) []byte {
		module = append(module, id)
		module = append(module, uleb(len(body))...)
		return append(module, body...)
	}
	vector := func(body, item []byte) []byte {
		body = append(body, uleb(len(item))...)
		return append(body, item...)
	}
	constant := []byte{0x41}
	remaining := iterations
	for {
		current := byte(remaining & 0x7f)
		remaining >>= 7
		more := remaining != 0 || current&0x40 != 0
		if more {
			current |= 0x80
		}
		constant = append(constant, current)
		if !more {
			break
		}
	}
	module := []byte{0, 'a', 's', 'm', 1, 0, 0, 0}
	types := []byte{2, 0x60, 1, 0x7f, 1, 0x7f, 0x60, 2, 0x7f, 0x7f, 1, 0x7e}
	module = section(module, 1, types)
	imports := []byte{1}
	imports = append(imports, name("liapoldus.service.v2")...)
	imports = append(imports, name("call")...)
	imports = append(imports, 0, 1)
	module = section(module, 2, imports)
	module = section(module, 3, []byte{2, 0, 1})
	module = section(module, 5, []byte{1, 0, 1})
	exports := []byte{3}
	exports = append(exports, name("memory")...)
	exports = append(exports, 2, 0)
	exports = append(exports, name("alloc")...)
	exports = append(exports, 0, 1)
	exports = append(exports, name("invoke")...)
	exports = append(exports, 0, 2)
	module = section(module, 7, exports)
	allocate := []byte{0, 0x41, 0, 0x0b}
	invoke := []byte{2, 1, 0x7f, 1, 0x7e, 0x20, 0, 0x20, 1, 0x10, 0, 0x21, 3}
	invoke = append(invoke, constant...)
	invoke = append(invoke, 0x21, 2, 0x03, 0x40, 0x20, 2, 0x41, 1, 0x6b, 0x22, 2,
		0x0d, 0, 0x0b, 0x20, 3, 0x0b)
	codes := []byte{2}
	codes = vector(codes, allocate)
	codes = vector(codes, invoke)
	module = section(module, 10, codes)
	return module
}

func TestExecutorRejectsUnrecognizedWasmImports(t *testing.T) {
	module, err := hex.DecodeString("0061736d01000000010c0260017f017f60027f7f017e021c01136c6961706f6c6475732e706c7567696e2e76320463616c6c000103030200010503010001071b03066d656d6f7279020005616c6c6f63000106696e766f6b6500020a0f02040041000b08002000200110000b")
	if err != nil {
		t.Fatal(err)
	}
	// Change only the import's field name from the approved host function.
	for index := 0; index+3 < len(module); index++ {
		if string(module[index:index+4]) == "call" {
			module[index] = 'n'
			break
		}
	}
	if err := wasm.ValidateModule(context.Background(), module); err == nil {
		t.Fatal("unrecognized import was admitted")
	}
}

func TestExecutorUsesConfiguredExportAndRejectsMissingExport(t *testing.T) {
	module, err := hex.DecodeString("0061736d01000000010c0260017f017f60027f7f017e03030200010503010001071b03066d656d6f7279020005616c6c6f63000006696e766f6b6500010a1302040041000b0c002001ad4220862000ad840b")
	if err != nil {
		t.Fatal(err)
	}
	if err := wasm.ValidateModuleExports(context.Background(), module, []string{"invoke"}); err != nil {
		t.Fatalf("valid configured export was rejected: %v", err)
	}
	if err := wasm.ValidateModuleExports(context.Background(), module, []string{"missing"}); !errors.Is(err, wasm.ErrInvalidABI) {
		t.Fatalf("missing configured export was accepted: %v", err)
	}
	if _, err := (wasm.Executor{}).ExecuteWithHostExport(context.Background(), module, "missing", []byte(`{}`), nil); !errors.Is(err, wasm.ErrInvalidABI) {
		t.Fatalf("missing export invocation error = %v", err)
	}
}
