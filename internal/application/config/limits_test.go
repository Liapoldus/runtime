package config_test

import (
	"fmt"
	"strings"
	"testing"

	application "github.com/Liapoldus/runtime/internal/application/config"
	"github.com/Liapoldus/runtime/internal/domain/models"
)

func TestConfigurationValidationBoundsAndRoutes(t *testing.T) {
	valid := func() models.Configuration { return validConfiguration() }
	for _, test := range []struct {
		name   string
		mutate func(*models.Configuration)
		want   string
	}{
		{"valid", func(*models.Configuration) {}, ""},
		{"version", func(c *models.Configuration) { c.SchemaVersion = "1" }, "invalid_configuration"},
		{"invalid digest", func(c *models.Configuration) { c.Modules[0].SHA256 = strings.Repeat("A", 64) }, "invalid_digest"},
		{"invalid module id", func(c *models.Configuration) { c.Modules[0].ID = "bad-id" }, "invalid_configuration"},
		{"invalid command", func(c *models.Configuration) { c.Commands[0].Export = "bad-export" }, "invalid_command"},
		{"duplicate command", func(c *models.Configuration) { c.Commands = append(c.Commands, c.Commands[0]) }, "duplicate_command"},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid()
			test.mutate(&candidate)
			if got := application.ValidateConfiguration(candidate); got != test.want {
				t.Fatalf("got %q; want %q", got, test.want)
			}
		})
	}
	for _, kind := range []string{"commands", "modules", "calls"} {
		bound := 128
		if kind == "modules" {
			bound = 64
		}
		if kind == "calls" {
			bound = 64
		}
		for _, size := range []int{bound, bound + 1} {
			candidate := valid()
			switch kind {
			case "commands":
				candidate.Commands = nil
				for i := range size {
					candidate.Commands = append(candidate.Commands, models.Command{Name: fmt.Sprintf("run_%d", i), ModuleID: "forms", Export: "invoke", Entities: []string{"entry"}})
				}
			case "modules":
				candidate.Commands = candidate.Commands[:1]
				candidate.Modules = nil
				for i := range size {
					id := fmt.Sprintf("module_%d", i)
					if i == 0 {
						id = "forms"
					}
					candidate.Modules = append(candidate.Modules, models.Module{ID: id, SHA256: fmt.Sprintf("%064x", i+1)})
				}
			case "calls":
				candidate.Commands[0].Calls = nil
				for i := range size {
					candidate.Commands[0].Calls = append(candidate.Commands[0].Calls, models.PluginMethod{Plugin: "domain", Method: fmt.Sprintf("domain.method_%d", i)})
				}
			}
			want := ""
			if size > bound {
				want = "invalid_command"
				if kind == "modules" || kind == "commands" {
					want = "invalid_configuration"
				}
			}
			if got := application.ValidateConfiguration(candidate); got != want {
				t.Fatalf("%s size %d: got %q; want %q", kind, size, got, want)
			}
		}
	}
}
