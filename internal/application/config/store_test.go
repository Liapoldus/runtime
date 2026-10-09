package config_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/Liapoldus/runtime/internal/application/config"
	"github.com/Liapoldus/runtime/internal/domain/models"
	"reflect"
	"testing"
)

func TestActivationPreservesExactBytesAndSnapshotIsolation(t *testing.T) {
	ctx := t.Context()
	store := &config.ConfigurationStore{}
	module := []byte("immutable module")
	digest := func(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
	settings := models.Configuration{SchemaVersion: "1", GroupID: "initial", ModelInstanceID: "domain", Module: models.Module{SHA256: digest(module)}, Commands: []models.Command{{Name: "submit", Export: "invoke", TenantSites: []string{"tenant/site"}, Entities: []string{"entries"}}}}
	initial, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Activate(ctx, "generation-1", "1", digest(initial), initial, module); err != nil {
		t.Fatal(err)
	}
	before, ok := store.Snapshot()
	if !ok {
		t.Fatal("missing active snapshot")
	}
	settings.GroupID = "candidate"
	candidate, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	for _, tc := range []struct {
		name         string
		ctx          context.Context
		version, sha string
		raw, module  []byte
	}{
		{"digest mismatch", ctx, "1", digest(initial), candidate, module},
		{"version mismatch", ctx, "2", digest(candidate), candidate, module},
		{"module mismatch", ctx, "1", digest(candidate), candidate, []byte("other")},
		{"cancelled", cancelled, "1", digest(candidate), candidate, module},
		{"malformed", ctx, "1", digest([]byte("{")), []byte("{"), module},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := store.Activate(tc.ctx, "generation-2", tc.version, tc.sha, tc.raw, tc.module); !errors.Is(err, config.ErrConfigurationRejected) {
				t.Fatal("candidate was not rejected")
			}
			after, ok := store.Snapshot()
			if !ok || !reflect.DeepEqual(before, after) {
				t.Fatal("rejected candidate replaced active pair")
			}
		})
	}
	module[0] = 'X'
	snapshot, _ := store.Snapshot()
	snapshot.Module[0] = 'Y'
	snapshot.Settings.Commands[0].TenantSites[0] = "other/site"
	snapshot.Settings.Commands[0].Entities[0] = "other"
	after, ok := store.Snapshot()
	if !ok || !reflect.DeepEqual(before, after) {
		t.Fatal("mutable caller data changed active pair")
	}
}
