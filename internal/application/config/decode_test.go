package config_test

import (
	"encoding/json"
	"github.com/Liapoldus/runtime/internal/application/config"
	"github.com/Liapoldus/runtime/internal/domain/models"
	"strings"
	"testing"
)

func TestDecodeConfiguration(t *testing.T) {
	valid := func() models.Configuration {
		return models.Configuration{SchemaVersion: "1", GroupID: "forms", ModelInstanceID: "model", Module: models.Module{SHA256: strings.Repeat("a", 64)}, Commands: []models.Command{{Name: "submit", Export: "invoke", TenantSites: []string{"tenant-a/site-1"}, Entities: []string{"entries"}}}}
	}
	for _, tc := range []struct {
		name   string
		change func(*models.Configuration)
		want   string
	}{
		{"valid", func(*models.Configuration) {}, ""},
		{"missing scope", func(c *models.Configuration) { c.Commands[0].TenantSites = nil }, "missing_scope"},
		{"duplicate command", func(c *models.Configuration) { c.Commands = append(c.Commands, c.Commands[0]) }, "duplicate_command"},
		{"invalid digest", func(c *models.Configuration) { c.Module.SHA256 = "../../module.wasm" }, "invalid_digest"},
		{"invalid group", func(c *models.Configuration) { c.GroupID = "invalid-group" }, "invalid_configuration"},
		{"invalid export", func(c *models.Configuration) { c.Commands[0].Export = "invoke-now" }, "invalid_command"},
		{"invalid scope", func(c *models.Configuration) { c.Commands[0].TenantSites = []string{"tenant.example/site"} }, "invalid_scope"},
		{"duplicate scope", func(c *models.Configuration) { c.Commands[0].TenantSites = []string{"tenant/site", "tenant/site"} }, "invalid_scope"},
		{"duplicate entity", func(c *models.Configuration) { c.Commands[0].Entities = []string{"entries", "entries"} }, "invalid_command"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := valid()
			tc.change(&candidate)
			raw, err := json.Marshal(candidate)
			if err != nil {
				t.Fatal(err)
			}
			_, code := config.DecodeConfiguration(raw)
			if code != tc.want {
				t.Fatalf("got %q, want %q", code, tc.want)
			}
		})
	}
	raw, err := json.Marshal(valid())
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{string(raw) + " {}", string(raw[:len(raw)-1]) + `,"unexpected":true}`, strings.Replace(string(raw), `"sha256":`, `"path":"/tmp/module.wasm","sha256":`, 1), "{", "null", "[]"} {
		if _, code := config.DecodeConfiguration([]byte(input)); code != "invalid_configuration" {
			t.Fatalf("unexpected result: %s", code)
		}
	}
}
