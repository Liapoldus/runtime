package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	sdkmodels "github.com/Liapoldus/plugin-sdk/v2/domain/models"
	"github.com/Liapoldus/runtime/internal/application/config"
	"github.com/Liapoldus/runtime/internal/domain/models"
	"github.com/Liapoldus/runtime/internal/infrastructure/artifacts"
)

func TestConfigurationApplierRejectsMissingCommandExportBeforeActivation(t *testing.T) {
	module, err := hex.DecodeString("0061736d01000000010c0260017f017f60027f7f017e03030200010503010001071b03066d656d6f7279020005616c6c6f63000006696e766f6b6500010a1302040041000b0c002001ad4220862000ad840b")
	if err != nil {
		t.Fatal(err)
	}
	moduleHash := sha256.Sum256(module)
	moduleDigest := hex.EncodeToString(moduleHash[:])
	artifactStore := artifacts.Store{Root: t.TempDir()}
	if err := artifactStore.Put(t.Context(), moduleDigest, bytes.NewReader(module)); err != nil {
		t.Fatal(err)
	}
	settings, err := json.Marshal(models.Configuration{
		SchemaVersion: "2",
		Modules:       []models.Module{{ID: "app", SHA256: moduleDigest}},
		Commands:      []models.Command{{Name: "submit", ModuleID: "app", Export: "dispatch", Entities: []string{"Entry"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	settingsHash := sha256.Sum256(settings)
	document, err := sdkmodels.NewConfiguration("generation-1", "2", hex.EncodeToString(settingsHash[:]), settings)
	if err != nil {
		t.Fatal(err)
	}
	store := &config.ConfigurationStore{}
	applier := configurationApplier{store: store, modules: artifactStore}
	if err := applier.Apply(context.Background(), document); err == nil {
		t.Fatal("configuration with a missing command export was accepted")
	}
	if store.Ready() {
		t.Fatal("configuration with a missing command export became active")
	}
}
