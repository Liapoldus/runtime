package config_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Liapoldus/runtime/internal/application/config"
	"github.com/Liapoldus/runtime/internal/domain/models"
)

func validConfiguration() models.Configuration {
	return models.Configuration{
		SchemaVersion: "2",
		Modules:       []models.Module{{ID: "forms", SHA256: strings.Repeat("a", 64)}, {ID: "notify", SHA256: strings.Repeat("b", 64)}},
		Commands: []models.Command{
			{Name: "submit", ModuleID: "forms", Export: "invoke", Entities: []string{"entries"}, Calls: []models.ServiceCall{{Service: "domain", Method: "domain.query"}}},
			{Name: "alert", ModuleID: "notify", Export: "invoke", Entities: []string{"notifications"}},
		},
	}
}

func TestDecodeConfigurationRejectsOldScopeAndSingleModuleShapes(t *testing.T) {
	valid := validConfiguration()
	for _, test := range []struct {
		name   string
		change func(*models.Configuration)
		want   string
	}{
		{"valid multi-module routes", func(*models.Configuration) {}, ""},
		{"missing module", func(c *models.Configuration) { c.Modules = nil }, "invalid_configuration"},
		{"bad digest", func(c *models.Configuration) { c.Modules[0].SHA256 = "../../artifact" }, "invalid_digest"},
		{"duplicate module id", func(c *models.Configuration) { c.Modules = append(c.Modules, c.Modules[0]) }, "invalid_configuration"},
		{"unknown module route", func(c *models.Configuration) { c.Commands[0].ModuleID = "missing" }, "invalid_command"},
		{"invalid export", func(c *models.Configuration) { c.Commands[0].Export = "invoke-now" }, "invalid_command"},
		{"duplicate command", func(c *models.Configuration) { c.Commands = append(c.Commands, c.Commands[0]) }, "duplicate_command"},
		{"duplicate service method", func(c *models.Configuration) {
			c.Commands[0].Calls = append(c.Commands[0].Calls, c.Commands[0].Calls[0])
		}, "invalid_command"},
		{"malformed method", func(c *models.Configuration) { c.Commands[0].Calls[0].Method = "domain..query" }, "invalid_command"},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			candidate.Modules = append([]models.Module(nil), valid.Modules...)
			candidate.Commands = append([]models.Command(nil), valid.Commands...)
			candidate.Commands[0].Calls = append([]models.ServiceCall(nil), valid.Commands[0].Calls...)
			test.change(&candidate)
			raw, err := json.Marshal(candidate)
			if err != nil {
				t.Fatal(err)
			}
			_, code := config.DecodeConfiguration(raw)
			if code != test.want {
				t.Fatalf("got %q, want %q", code, test.want)
			}
		})
	}
	raw, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{string(raw) + " {}", string(raw[:len(raw)-1]) + `,"unexpected":true}`, strings.Replace(string(raw), `"modules"`, `"module"`, 1), "{", "null", "[]"} {
		if _, code := config.DecodeConfiguration([]byte(input)); code != "invalid_configuration" {
			t.Fatalf("unexpected result: %s", code)
		}
	}
}

func TestDecodeConfigurationUsesServiceTargetWithoutChangingIDsOrMethods(t *testing.T) {
	raw := []byte(`{"schemaVersion":"2","modules":[{"moduleId":"forms","sha256":"` + strings.Repeat("a", 64) + `"}],"commands":[{"name":"submit","moduleId":"forms","export":"invoke","entities":["entries"],"calls":[{"service":"domain","method":"domain.query"}]}]}`)
	if _, code := config.DecodeConfiguration(raw); code != "" {
		t.Fatalf("service target with existing ID and method was rejected: %s", code)
	}
	legacy := strings.Replace(string(raw), `"service"`, `"plugin"`, 1)
	if _, code := config.DecodeConfiguration([]byte(legacy)); code == "" {
		t.Fatal("legacy plugin target field remains accepted")
	}
}
