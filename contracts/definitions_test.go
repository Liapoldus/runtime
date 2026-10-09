package contracts_test

import (
	"bytes"
	"encoding/json"
	"github.com/Liapoldus/runtime/contracts"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func readContract(t *testing.T, name string, target any) {
	t.Helper()
	//nolint:gosec // G304: reads known contract filenames in the repository artifact directory.
	raw, err := os.ReadFile(filepath.Join("v1", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatal(err)
	}
}

func TestInvokeDefinitionsMatchPublishedDocument(t *testing.T) {
	var published contracts.InvokeErrors
	readContract(t, "errors-invoke.json", &published)
	actual := contracts.InvokeErrorDefinitions()
	if !reflect.DeepEqual(actual, published) {
		t.Fatal("invoke definitions differ from published document")
	}
	for code, problem := range published.Errors {
		status, retryable := contracts.InvokeErrorCode(code)
		if status != problem.HTTP || retryable != problem.Retryable {
			t.Fatalf("mapping changed for %s", code)
		}
	}
	for _, code := range []string{"", "unknown", "INVALID_REQUEST"} {
		status, retryable := contracts.InvokeErrorCode(code)
		if status != 502 || retryable {
			t.Fatalf("unknown-code fallback changed for %q", code)
		}
	}
}

func TestModuleActionDefinitionsMatchPublishedDocument(t *testing.T) {
	var published map[string]contracts.AdminAction
	readContract(t, "admin-actions.json", &published)
	actual := contracts.ModuleAdminActionDefinitions()
	if len(published) != 2 {
		t.Fatal("unexpected published module action count")
	}
	for _, action := range []contracts.AdminAction{actual.List, actual.Publish} {
		want, ok := published[action.Capability]
		if !ok {
			t.Fatalf("unknown action: %s", action.Capability)
		}
		want.Capability = action.Capability
		if !reflect.DeepEqual(action, want) {
			t.Fatalf("action definition changed: %s", action.Capability)
		}
	}
}

func TestDefinitionsDoNotShareMutableMaps(t *testing.T) {
	first := contracts.InvokeErrorDefinitions()
	delete(first.Errors, "not_ready")
	second := contracts.InvokeErrorDefinitions()
	if problem, ok := second.Errors["not_ready"]; !ok || problem.HTTP != 503 || !problem.Retryable {
		t.Fatal("invoke definitions share mutable state")
	}
	status, retryable := contracts.InvokeErrorCode("not_ready")
	if status != 503 || !retryable {
		t.Fatal("caller mutation changed runtime mapping")
	}
	actions := contracts.ModuleAdminActionDefinitions()
	delete(actions.List.Errors, "invalid_input")
	actions.Publish.Errors["invalid_input"] = contracts.AdminActionProblem{HTTP: 599, Code: "changed"}
	fresh := contracts.ModuleAdminActionDefinitions()
	for _, action := range []contracts.AdminAction{fresh.List, fresh.Publish} {
		if problem := action.Errors["invalid_input"]; problem.HTTP != 400 || problem.Code != "invalid_input" {
			t.Fatal("module actions share mutable definitions")
		}
	}
}

func TestGeneratedMetadataAndABI(t *testing.T) {
	metadata, err := contracts.PluginDocuments()
	if err != nil {
		t.Fatal(err)
	}
	for name, actual := range map[string][]byte{
		"plugin.json": metadata.Manifest, "settings.schema.json": metadata.ConfigurationSchema, "admin-surface.json": metadata.AdminSurface,
	} {
		//nolint:gosec // G304: reads known contract filenames in the repository artifact directory.
		published, err := os.ReadFile(filepath.Join("v1", name))
		if err != nil {
			t.Fatal(err)
		}
		if string(actual) != string(published) {
			t.Fatalf("metadata bytes changed: %s", name)
		}
	}
	var published contracts.WasmABI
	readContract(t, "wasm-abi.json", &published)
	actual := contracts.WasmABIDefinition()
	if !reflect.DeepEqual(actual, published) {
		t.Fatal("WASM ABI differs from generated artifact")
	}
}

func TestArtifactSetIsReproducible(t *testing.T) {
	entries, err := os.ReadDir("v1")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(contracts.Documents()) {
		t.Fatal("artifact set differs")
	}
	for _, entry := range entries {
		t.Run(entry.Name(), func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join("v1", entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			first, err := contracts.Document(entry.Name())
			if err != nil {
				t.Fatal(err)
			}
			second, err := contracts.Document(entry.Name())
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(first, want) || !bytes.Equal(first, second) {
				t.Fatal("non-reproducible artifact")
			}
		})
	}
}
