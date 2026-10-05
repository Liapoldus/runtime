package application

import (
	"encoding/hex"
	"strings"

	"github.com/Liapoldus/runtime/internal/domain/models"
)

// ValidateConfiguration checks product ownership and scope boundaries before
// a candidate can be compiled or activated.
func ValidateConfiguration(config models.Configuration) string {
	if config.SchemaVersion != "1" || config.GroupID == "" || config.ModelInstanceID == "" || len(config.Commands) == 0 {
		return "invalid_configuration"
	}
	if len(config.Module.SHA256) != 64 || strings.ToLower(config.Module.SHA256) != config.Module.SHA256 {
		return "invalid_digest"
	}
	if _, err := hex.DecodeString(config.Module.SHA256); err != nil {
		return "invalid_digest"
	}
	seen := make(map[string]struct{}, len(config.Commands))
	for _, command := range config.Commands {
		if command.Name == "" || command.Export == "" || len(command.Entities) == 0 {
			return "invalid_command"
		}
		if len(command.TenantSites) == 0 {
			return "missing_scope"
		}
		for _, scope := range command.TenantSites {
			parts := strings.Split(scope, "/")
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				return "invalid_scope"
			}
		}
		if _, duplicate := seen[command.Name]; duplicate {
			return "duplicate_command"
		}
		seen[command.Name] = struct{}{}
	}
	return ""
}
