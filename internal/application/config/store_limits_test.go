package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Liapoldus/runtime/internal/domain/models"
)

type cancelOnErrContext struct {
	context.Context
	calls     int
	cancelAt  int
	cancelNow context.CancelFunc
}

func (ctx *cancelOnErrContext) Err() error {
	ctx.calls++
	if ctx.calls == ctx.cancelAt {
		ctx.cancelNow()
	}
	return ctx.Context.Err()
}

func TestValidateModuleAggregateBytes(t *testing.T) {
	for _, test := range []struct {
		name    string
		modules map[string][]byte
		maximum int64
		wantErr bool
	}{
		{name: "exact limit accepted", modules: map[string][]byte{"first": make([]byte, 6), "second": make([]byte, 4)}, maximum: 10},
		{name: "over limit rejected", modules: map[string][]byte{"first": make([]byte, 6), "second": make([]byte, 5)}, maximum: 10, wantErr: true},
		{name: "empty module set accepted", modules: map[string][]byte{}, maximum: 10},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateModuleAggregateBytes(test.modules, test.maximum)
			if test.wantErr && !errors.Is(err, ErrConfigurationRejected) {
				t.Fatalf("expected aggregate-limit rejection, got %v", err)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("unexpected aggregate-limit error: %v", err)
			}
		})
	}
}

func TestActivateRejectsOversizedGenerationAndKeepsPrevious(t *testing.T) {
	digest := func(value []byte) string {
		sum := sha256.Sum256(value)
		return hex.EncodeToString(sum[:])
	}
	document := func(modules []models.Module) []byte {
		raw, err := json.Marshal(models.Configuration{
			SchemaVersion: "2", Modules: modules,
			Commands: []models.Command{{Name: "run", ModuleID: "app", Export: "run", Entities: []string{"Entry"}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	store := &ConfigurationStore{}
	activeModule := []byte("active")
	activeSettings := document([]models.Module{{ID: "app", SHA256: digest(activeModule)}})
	if err := store.activate(context.Background(), "generation-1", "2", digest(activeSettings), activeSettings,
		map[string][]byte{"app": activeModule}, 10); err != nil {
		t.Fatal(err)
	}
	before, found, err := captureModule(store)
	if err != nil || !found {
		t.Fatalf("capture current generation: found=%v err=%v", found, err)
	}
	first, second := []byte("123456"), []byte("12345")
	candidate := document([]models.Module{{ID: "app", SHA256: digest(first)}, {ID: "extra", SHA256: digest(second)}})
	err = store.activate(context.Background(), "generation-2", "2", digest(candidate), candidate,
		map[string][]byte{"app": first, "extra": second}, 10)
	if !errors.Is(err, ErrConfigurationRejected) {
		t.Fatalf("oversized candidate was accepted: %v", err)
	}
	after, found, err := captureModule(store)
	if err != nil || !found || before.Generation != after.Generation || before.SHA256 != after.SHA256 ||
		string(before.Module) != string(after.Module) {
		t.Fatalf("rejected generation replaced active state: before=%#v after=%#v found=%v err=%v", before, after, found, err)
	}
}

func TestActivateStopsWhenCandidateContextIsCancelled(t *testing.T) {
	digest := func(value []byte) string {
		sum := sha256.Sum256(value)
		return hex.EncodeToString(sum[:])
	}
	module := []byte("same module")
	settings, err := json.Marshal(models.Configuration{
		SchemaVersion: "2", Modules: []models.Module{{ID: "app", SHA256: digest(module)}},
		Commands: []models.Command{{Name: "run", ModuleID: "app", Export: "run", Entities: []string{"Entry"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &ConfigurationStore{}
	if err := store.Activate(context.Background(), "generation-1", "2", digest(settings), settings, map[string][]byte{"app": module}); err != nil {
		t.Fatal(err)
	}
	before, found, err := captureModule(store)
	if err != nil || !found {
		t.Fatalf("capture current generation: found=%v err=%v", found, err)
	}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelOnErrContext{Context: base, cancelAt: 3, cancelNow: cancel}
	err = store.activate(ctx, "generation-2", "2", digest(settings), settings, map[string][]byte{"app": module}, 64)
	if !errors.Is(err, ErrConfigurationRejected) {
		t.Fatalf("cancelled candidate was not rejected: %v", err)
	}
	after, found, err := captureModule(store)
	if err != nil || !found || before.Generation != after.Generation || before.SHA256 != after.SHA256 {
		t.Fatalf("cancelled candidate replaced active generation: before=%#v after=%#v found=%v err=%v", before, after, found, err)
	}
}

func captureModule(store *ConfigurationStore) (CommandView, bool, error) {
	var captured CommandView
	found, err := store.WithCommand("run", func(view CommandView) error {
		view.Module = append([]byte(nil), view.Module...)
		captured = view
		return nil
	})
	return captured, found, err
}
