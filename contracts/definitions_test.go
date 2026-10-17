package contracts_test

import (
	"bytes"
	"encoding/json"
	"github.com/Liapoldus/runtime/contracts"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
)

func readContract(t *testing.T, name string, target any) {
	t.Helper()
	//nolint:gosec // G304: reads known contract filenames in the repository artifact directory.
	raw, err := os.ReadFile(filepath.Join("v2", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatal(err)
	}
}

func schemaObject(t *testing.T, parent map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := parent[key].(map[string]any)
	if !ok {
		t.Fatalf("schema node %q is missing or not an object", key)
	}
	return value
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
	metadata, err := contracts.RuntimeDocuments()
	if err != nil {
		t.Fatal(err)
	}
	for name, actual := range map[string][]byte{
		"manifest.json": metadata.Manifest, "settings.schema.json": metadata.ConfigurationSchema, "admin-surface.json": metadata.AdminSurface,
	} {
		//nolint:gosec // G304: reads known contract filenames in the repository artifact directory.
		published, err := os.ReadFile(filepath.Join("v2", name))
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

func TestVersionedRuntimeSchemasCarryV2Titles(t *testing.T) {
	for name, expected := range map[string]string{
		"invoke-success.schema.json":           "Runtime v2 invoke success body",
		"module-artifact-metadata.schema.json": "Runtime module upload metadata v2",
		"module-artifact-receipt.schema.json":  "Runtime module upload receipt v2",
	} {
		t.Run(name, func(t *testing.T) {
			var document map[string]any
			readContract(t, name, &document)
			if document["title"] != expected {
				t.Fatalf("schema title = %v, want %q", document["title"], expected)
			}
		})
	}
}

func TestSettingsSchemaAllowsGenericServiceMethods(t *testing.T) {
	raw, err := contracts.Document("settings.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	properties := schemaObject(t, document, "properties")
	commands := schemaObject(t, properties, "commands")
	commandItems := schemaObject(t, commands, "items")
	commandProperties := schemaObject(t, commandItems, "properties")
	calls := schemaObject(t, commandProperties, "calls")
	callItems := schemaObject(t, calls, "items")
	callProperties := schemaObject(t, callItems, "properties")
	service, exists := callProperties["service"].(map[string]any)
	if !exists {
		t.Fatal("settings schema is missing the service target field")
	}
	if _, exists := callProperties["plugin"]; exists {
		t.Fatal("settings schema still accepts the retired plugin target field")
	}
	if service["pattern"] != "^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$" {
		t.Fatalf("service target pattern changed: %v", service["pattern"])
	}
	method := schemaObject(t, callProperties, "method")
	pattern, ok := method["pattern"].(string)
	if !ok {
		t.Fatal("service method schema has no pattern")
	}
	if method["maxLength"] != float64(128) {
		t.Fatalf("service method schema maxLength = %v, want 128", method["maxLength"])
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatalf("service method schema pattern is invalid: %v", err)
	}
	for _, value := range []string{"domain.transaction", "metrics.aggregate"} {
		if !compiled.MatchString(value) {
			t.Errorf("settings schema rejects generic service method %q", value)
		}
	}
	for _, value := range []string{"metrics.aggregate.v2", "metrics.aggregate-now"} {
		if compiled.MatchString(value) {
			t.Errorf("settings schema accepts unsupported service method %q", value)
		}
	}
}

func TestArtifactSetIsReproducible(t *testing.T) {
	entries, err := os.ReadDir("v2")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(contracts.Documents()) {
		t.Fatal("artifact set differs")
	}
	for _, entry := range entries {
		t.Run(entry.Name(), func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join("v2", entry.Name()))
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
