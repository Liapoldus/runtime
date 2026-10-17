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
	otherModule := []byte("second immutable module")
	digest := func(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
	settings := models.Configuration{SchemaVersion: "2", Modules: []models.Module{{ID: "forms", SHA256: digest(module)}, {ID: "notify", SHA256: digest(otherModule)}}, Commands: []models.Command{
		{Name: "submit", ModuleID: "forms", Export: "invoke", Entities: []string{"entries"}},
		{Name: "send", ModuleID: "notify", Export: "invoke", Entities: []string{"users"}},
	}}
	initial, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	modules := map[string][]byte{"forms": module, "notify": otherModule}
	if err := store.Activate(ctx, "generation-1", "2", digest(initial), initial, modules); err != nil {
		t.Fatal(err)
	}
	before := captureCommandViews(t, store, "submit", "send")
	settings.Commands[0].Export = "candidate"
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
		raw          []byte
		modules      map[string][]byte
	}{
		{"digest mismatch", ctx, "2", digest(initial), candidate, modules},
		{"version mismatch", ctx, "1", digest(candidate), candidate, modules},
		{"module mismatch", ctx, "2", digest(candidate), candidate, map[string][]byte{"forms": []byte("wrong"), "notify": otherModule}},
		{"missing module", ctx, "2", digest(candidate), candidate, map[string][]byte{"forms": module}},
		{"cancelled", cancelled, "2", digest(candidate), candidate, modules},
		{"malformed", ctx, "2", digest([]byte("{")), []byte("{"), modules},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := store.Activate(tc.ctx, "generation-2", tc.version, tc.sha, tc.raw, tc.modules); !errors.Is(err, config.ErrConfigurationRejected) {
				t.Fatal("candidate was not rejected")
			}
			after := captureCommandViews(t, store, "submit", "send")
			if !reflect.DeepEqual(before, after) {
				t.Fatal("rejected candidate replaced active pair")
			}
		})
	}
	module[0] = 'X'
	otherModule[0] = 'Z'
	if _, err := store.WithCommand("submit", func(view config.CommandView) error {
		view.Command.Entities[0] = "mutated"
		view.Command.Calls = []models.ServiceCall{{Service: "domain", Method: "domain.get"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	after := captureCommandViews(t, store, "submit", "send")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("mutable caller data changed active pair")
	}
}

type capturedCommandView struct {
	Generation    string
	SchemaVersion string
	SHA256        string
	Command       models.Command
	Module        []byte
}

func captureCommandViews(t *testing.T, store *config.ConfigurationStore, names ...string) map[string]capturedCommandView {
	t.Helper()
	result := make(map[string]capturedCommandView, len(names))
	for _, name := range names {
		found, err := store.WithCommand(name, func(view config.CommandView) error {
			view.Module = append([]byte(nil), view.Module...)
			result[name] = capturedCommandView{
				Generation: view.Generation, SchemaVersion: view.SchemaVersion, SHA256: view.SHA256,
				Command: view.Command, Module: view.Module,
			}
			return nil
		})
		if err != nil || !found {
			t.Fatalf("capture command %s: found=%v err=%v", name, found, err)
		}
	}
	return result
}
