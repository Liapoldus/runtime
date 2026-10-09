package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync/atomic"

	"github.com/Liapoldus/runtime/internal/domain/models"
)

const (
	messageConfigurationRejected string = "Runtime configuration rejected"
)

//nolint:staticcheck // ST1005: preserve the existing rejection token and sentinel text.
var ErrConfigurationRejected = errors.New(messageConfigurationRejected)

// ConfigurationSnapshot is one completely validated configuration generation.
// A caller receives a copy so mutable slices cannot change the active snapshot.
type ConfigurationSnapshot struct {
	Generation    string
	SchemaVersion string
	SHA256        string
	Settings      models.Configuration
	Module        []byte
}

// ConfigurationStore is the in-memory active Runtime configuration. Candidate
// settings are decoded and validated before one atomic pointer swap makes them
// visible to readers.
type ConfigurationStore struct {
	active atomic.Pointer[ConfigurationSnapshot]
}

// Activate validates one exact SDK-pulled document before making it active.
// Any validation failure or cancellation preserves the previous snapshot.
func (store *ConfigurationStore) Activate(ctx context.Context, generation, schemaVersion, digest string, rawJSON, module []byte) error {
	if store == nil || ctx == nil || ctx.Err() != nil || generation == "" || schemaVersion == "" {
		return ErrConfigurationRejected
	}
	actualDigest := sha256.Sum256(rawJSON)
	if digest != hex.EncodeToString(actualDigest[:]) {
		return ErrConfigurationRejected
	}
	settings, code := DecodeConfiguration(rawJSON)
	moduleDigest := sha256.Sum256(module)
	if code != "" || settings.SchemaVersion != schemaVersion || len(module) == 0 ||
		hex.EncodeToString(moduleDigest[:]) != settings.Module.SHA256 || ctx.Err() != nil {
		return ErrConfigurationRejected
	}
	candidate := &ConfigurationSnapshot{
		Generation:    generation,
		SchemaVersion: schemaVersion,
		SHA256:        digest,
		Settings:      settings,
		Module:        append([]byte(nil), module...),
	}
	store.active.Store(candidate)
	return nil
}

// Snapshot returns a stable copy of the currently active configuration.
func (store *ConfigurationStore) Snapshot() (ConfigurationSnapshot, bool) {
	if store == nil {
		return ConfigurationSnapshot{}, false
	}
	active := store.active.Load()
	if active == nil {
		return ConfigurationSnapshot{}, false
	}
	copy := *active
	copy.Module = append([]byte(nil), active.Module...)
	copy.Settings.Commands = append([]models.Command(nil), active.Settings.Commands...)
	for index := range copy.Settings.Commands {
		copy.Settings.Commands[index].TenantSites = append([]string(nil), active.Settings.Commands[index].TenantSites...)
		copy.Settings.Commands[index].Entities = append([]string(nil), active.Settings.Commands[index].Entities...)
	}
	return copy, true
}
