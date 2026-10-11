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

// ValidateConfiguration checks module, command and target references before a
// candidate can be compiled or activated.
func ValidateConfiguration(config models.Configuration) string {
	if config.SchemaVersion != configurationSchemaVersion || len(config.Modules) == 0 || len(config.Modules) > configurationMaxModules || len(config.Commands) == 0 || len(config.Commands) > configurationMaxCommands {
		return codeInvalidConfiguration
	}
	modules := make(map[string]struct{}, len(config.Modules))
	for _, module := range config.Modules {
		if !ValidIdentifier(module.ID) {
			return codeInvalidConfiguration
		}
		if len(module.SHA256) != moduleDigestHexBytes || strings.ToLower(module.SHA256) != module.SHA256 {
			return codeInvalidDigest
		}
		if _, err := hex.DecodeString(module.SHA256); err != nil {
			return codeInvalidDigest
		}
		if _, duplicate := modules[module.ID]; duplicate {
			return codeInvalidConfiguration
		}
		modules[module.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(config.Commands))
	for _, command := range config.Commands {
		if !ValidIdentifier(command.Name) || !ValidIdentifier(command.ModuleID) || !ValidIdentifier(command.Export) {
			return codeInvalidCommand
		}
		if _, found := modules[command.ModuleID]; !found {
			return codeInvalidCommand
		}
		if len(command.Entities) == 0 || len(command.Entities) > commandMaxEntities {
			return codeInvalidCommand
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
		if len(command.Calls) > commandMaxCalls {
			return codeInvalidCommand
		}
		seenCalls := make(map[string]struct{}, len(command.Calls))
		for _, call := range command.Calls {
			if !ValidPluginTarget(call.Plugin) || !ValidPluginMethod(call.Method) {
				return codeInvalidCommand
			}
			key := call.Plugin + "\x00" + call.Method
			if _, duplicate := seenCalls[key]; duplicate {
				return codeInvalidCommand
			}
			seenCalls[key] = struct{}{}
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

func ValidPluginTarget(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '_' || character == '.' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func ValidPluginMethod(value string) bool {
	if len(value) < 3 || len(value) > 128 {
		return false
	}
	dot := strings.IndexByte(value, '.')
	if dot < 1 || dot == len(value)-1 || strings.IndexByte(value[dot+1:], '.') >= 0 {
		return false
	}
	if !ValidIdentifier(value[:dot]) || !ValidIdentifier(value[dot+1:]) {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character == '.' || character == '_' || character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' {
			continue
		}
		return false
	}
	return true
}
