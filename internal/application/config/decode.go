package config

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"

	"github.com/Liapoldus/runtime/internal/domain/models"
)

// DecodeConfiguration enforces the versioned settings document shape before
// converting it to the application model. Unknown fields and trailing JSON
// values are rejected rather than silently ignored.
func DecodeConfiguration(raw []byte) (models.Configuration, string) {
	var config models.Configuration
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return models.Configuration{}, codeInvalidConfiguration
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return models.Configuration{}, codeInvalidConfiguration
	}
	if code := ValidateConfiguration(config); code != "" {
		return models.Configuration{}, code
	}
	return config, ""
}

// ValidateConfiguration checks product ownership and scope boundaries before
// a candidate can be compiled or activated.
func ValidateConfiguration(config models.Configuration) string {
	if config.SchemaVersion != configurationSchemaVersion || !ValidIdentifier(config.GroupID) || !ValidIdentifier(config.ModelInstanceID) || len(config.Commands) == 0 || len(config.Commands) > configurationMaxCommands {
		return codeInvalidConfiguration
	}
	if len(config.Module.SHA256) != moduleDigestHexBytes || strings.ToLower(config.Module.SHA256) != config.Module.SHA256 {
		return codeInvalidDigest
	}
	if _, err := hex.DecodeString(config.Module.SHA256); err != nil {
		return codeInvalidDigest
	}
	seen := make(map[string]struct{}, len(config.Commands))
	for _, command := range config.Commands {
		if !ValidIdentifier(command.Name) || !ValidIdentifier(command.Export) || len(command.Entities) == 0 || len(command.Entities) > commandMaxEntities {
			return codeInvalidCommand
		}
		if len(command.TenantSites) == 0 || len(command.TenantSites) > commandMaxTenantSites {
			return codeMissingScope
		}
		seenScopes := make(map[string]struct{}, len(command.TenantSites))
		for _, scope := range command.TenantSites {
			if !ValidTenantSite(scope) {
				return codeInvalidScope
			}
			if _, duplicate := seenScopes[scope]; duplicate {
				return codeInvalidScope
			}
			seenScopes[scope] = struct{}{}
		}
		seenEntities := make(map[string]struct{}, len(command.Entities))
		for _, entity := range command.Entities {
			if !ValidIdentifier(entity) {
				return codeInvalidCommand
			}
			if _, duplicate := seenEntities[entity]; duplicate {
				return codeInvalidCommand
			}
			seenEntities[entity] = struct{}{}
		}
		if _, duplicate := seen[command.Name]; duplicate {
			return codeDuplicateCommand
		}
		seen[command.Name] = struct{}{}
	}
	return ""
}

func ValidIdentifier(value string) bool {
	if len(value) == 0 || len(value) > identifierMaxBytes || !asciiAlpha(value[0]) {
		return false
	}
	for index := 1; index < len(value); index++ {
		character := value[index]
		if asciiAlpha(character) || character >= '0' && character <= '9' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func asciiAlpha(character byte) bool {
	return character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z'
}

func ValidTenantSite(value string) bool {
	parts := strings.Split(value, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	for _, part := range parts {
		for index := 0; index < len(part); index++ {
			character := part[index]
			if character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '_' || character == '-' {
				continue
			}
			return false
		}
	}
	return true
}
