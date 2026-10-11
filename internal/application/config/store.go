package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync/atomic"

	"github.com/Liapoldus/runtime/contracts"
	"github.com/Liapoldus/runtime/internal/domain/models"
)

const (
	messageConfigurationRejected string = "Runtime configuration rejected"
)

//nolint:staticcheck // ST1005: preserve the existing rejection token and sentinel text.
var ErrConfigurationRejected = errors.New(messageConfigurationRejected)

type configurationGeneration struct {
	Generation    string
	SchemaVersion string
	SHA256        string
	Settings      models.Configuration
	Modules       map[string][]byte
}

// CommandView pins one active generation and selects only the module needed by
// a command. Module must be treated as read-only and must not outlive the
// synchronous WithCommand callback.
type CommandView struct {
	Generation    string
	SchemaVersion string
	SHA256        string
	Command       models.Command
	ModuleDigest  string
	Module        []byte
}

// ConfigurationStore is the in-memory active Runtime configuration. Candidate
// settings are decoded and validated before one atomic pointer swap makes them
// visible to readers.
type ConfigurationStore struct {
	active atomic.Pointer[configurationGeneration]
}

func (store *ConfigurationStore) Ready() bool {
	return store != nil && store.active.Load() != nil
}

// Activate validates one exact SDK-pulled document before making it active.
// Any validation failure or cancellation preserves the previous snapshot.
func (store *ConfigurationStore) Activate(ctx context.Context, generation, schemaVersion, digest string, rawJSON []byte, modules map[string][]byte) error {
	return store.activate(ctx, generation, schemaVersion, digest, rawJSON, modules,
		int64(contracts.WasmABIDefinition().Limits.MaxGenerationArtifactBytes))
}

func (store *ConfigurationStore) activate(ctx context.Context, generation, schemaVersion, digest string, rawJSON []byte, modules map[string][]byte, maxGenerationBytes int64) error {
	if store == nil || ctx == nil || ctx.Err() != nil || generation == "" || schemaVersion == "" {
		return ErrConfigurationRejected
	}
	actualDigest := sha256.Sum256(rawJSON)
	if digest != hex.EncodeToString(actualDigest[:]) {
		return ErrConfigurationRejected
	}
	settings, code := DecodeConfiguration(rawJSON)
	if code != "" || settings.SchemaVersion != schemaVersion || len(modules) != len(settings.Modules) || ctx.Err() != nil {
		return ErrConfigurationRejected
	}
	if err := validateModuleAggregateBytes(modules, maxGenerationBytes); err != nil {
		return err
	}
	moduleCopies := make(map[string][]byte, len(modules))
	for _, declaration := range settings.Modules {
		if ctx.Err() != nil {
			return ErrConfigurationRejected
		}
		module := modules[declaration.ID]
		moduleDigest := sha256.Sum256(module)
		if len(module) == 0 || hex.EncodeToString(moduleDigest[:]) != declaration.SHA256 {
			return ErrConfigurationRejected
		}
		moduleCopies[declaration.ID] = append([]byte(nil), module...)
		if ctx.Err() != nil {
			return ErrConfigurationRejected
		}
	}
	if ctx.Err() != nil {
		return ErrConfigurationRejected
	}
	candidate := &configurationGeneration{
		Generation:    generation,
		SchemaVersion: schemaVersion,
		SHA256:        digest,
		Settings:      settings,
		Modules:       moduleCopies,
	}
	store.active.Store(candidate)
	return nil
}

func validateModuleAggregateBytes(modules map[string][]byte, maximum int64) error {
	if maximum < 0 {
		return ErrConfigurationRejected
	}
	var total int64
	for _, module := range modules {
		if int64(len(module)) > maximum-total {
			return ErrConfigurationRejected
		}
		total += int64(len(module))
	}
	return nil
}

// WithCommand reads one command and its module from a single immutable
// generation. The callback runs synchronously and must not mutate or retain
// the module bytes; keeping the active pointer alive guarantees a concurrent
// activation cannot invalidate them. This avoids cloning unrelated artifacts
// on every invocation.
func (store *ConfigurationStore) WithCommand(name string, use func(CommandView) error) (bool, error) {
	if store == nil || name == "" || use == nil {
		return false, ErrConfigurationRejected
	}
	active := store.active.Load()
	if active == nil {
		return false, nil
	}
	for _, configured := range active.Settings.Commands {
		if configured.Name != name {
			continue
		}
		command := configured
		command.Entities = append([]string(nil), configured.Entities...)
		command.Calls = append([]models.PluginMethod(nil), configured.Calls...)
		module := active.Modules[command.ModuleID]
		if len(module) == 0 {
			return true, ErrConfigurationRejected
		}
		moduleDigest := ""
		for _, declaration := range active.Settings.Modules {
			if declaration.ID == command.ModuleID {
				moduleDigest = declaration.SHA256
				break
			}
		}
		if moduleDigest == "" {
			return true, ErrConfigurationRejected
		}
		return true, use(CommandView{
			Generation: active.Generation, SchemaVersion: active.SchemaVersion, SHA256: active.SHA256,
			Command: command, ModuleDigest: moduleDigest, Module: module,
		})
	}
	return false, nil
}
