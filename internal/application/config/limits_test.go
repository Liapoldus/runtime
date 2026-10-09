package config_test

import (
	"fmt"
	application "github.com/Liapoldus/runtime/internal/application/config"
	"github.com/Liapoldus/runtime/internal/domain/models"
	"strings"
	"testing"
)

func TestConfigurationValidationTokensAndBounds(t *testing.T) {
	valid := func() models.Configuration {
		return models.Configuration{SchemaVersion: "1", GroupID: "group", ModelInstanceID: "model", Module: models.Module{SHA256: strings.Repeat("a", 64)}, Commands: []models.Command{{Name: "run", Export: "invoke", TenantSites: []string{"tenant/site"}, Entities: []string{"entry"}}}}
	}
	for _, test := range []struct {
		name   string
		mutate func(*models.Configuration)
		want   string
	}{
		{"valid", func(*models.Configuration) {}, ""},
		{"version", func(c *models.Configuration) { c.SchemaVersion = "2" }, "invalid_configuration"},
		{"digest", func(c *models.Configuration) { c.Module.SHA256 = strings.Repeat("A", 64) }, "invalid_digest"},
		{"command", func(c *models.Configuration) { c.Commands[0].Export = "bad-export" }, "invalid_command"},
		{"missing scope", func(c *models.Configuration) { c.Commands[0].TenantSites = nil }, "missing_scope"},
		{"scope", func(c *models.Configuration) { c.Commands[0].TenantSites = []string{"tenant/site/extra"} }, "invalid_scope"},
		{"duplicate", func(c *models.Configuration) { c.Commands = append(c.Commands, c.Commands[0]) }, "duplicate_command"},
		{"identifier at bound", func(c *models.Configuration) { c.GroupID = strings.Repeat("a", 64) }, ""},
		{"identifier over bound", func(c *models.Configuration) { c.GroupID = strings.Repeat("a", 65) }, "invalid_configuration"},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := valid()
			test.mutate(&config)
			if got := application.ValidateConfiguration(config); got != test.want {
				t.Fatalf("got %q; want %q", got, test.want)
			}
		})
	}
	for _, kind := range []string{"commands", "entities", "scopes"} {
		bound, overCode := 128, "invalid_command"
		if kind == "commands" {
			overCode = "invalid_configuration"
		}
		if kind == "scopes" {
			bound, overCode = 256, "missing_scope"
		}
		for _, size := range []int{bound, bound + 1} {
			config := valid()
			base := config.Commands[0]
			switch kind {
			case "commands":
				config.Commands = nil
				for i := range size {
					command := base
					command.Name = fmt.Sprintf("run_%d", i)
					config.Commands = append(config.Commands, command)
				}
			case "entities":
				config.Commands[0].Entities = nil
				for i := range size {
					config.Commands[0].Entities = append(config.Commands[0].Entities, fmt.Sprintf("entry_%d", i))
				}
			case "scopes":
				config.Commands[0].TenantSites = nil
				for i := range size {
					config.Commands[0].TenantSites = append(config.Commands[0].TenantSites, fmt.Sprintf("tenant/site_%d", i))
				}
			}
			want := ""
			if size > bound {
				want = overCode
			}
			if got := application.ValidateConfiguration(config); got != want {
				t.Fatalf("%s size %d: got %q; want %q", kind, size, got, want)
			}
		}
	}
}
