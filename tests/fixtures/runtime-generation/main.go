package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Liapoldus/service-sdk/v2/domain/models"
	runtimeconfig "github.com/Liapoldus/runtime/internal/application/config"
	"github.com/Liapoldus/runtime/internal/infrastructure/artifacts"
)

var abiModule = mustDecode("0061736d01000000010c0260017f017f60027f7f017e03030200010503010001071b03066d656d6f7279020005616c6c6f63000006696e766f6b6500010a1302040041000b0c002001ad4220862000ad840b")

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	ctx := context.Background()
	store := artifacts.Store{Root: os.Args[1]}
	configurationStore := &runtimeconfig.ConfigurationStore{}
	firstDigest := digest(abiModule)
	if err := store.Put(ctx, firstDigest, bytes.NewReader(abiModule)); err != nil {
		fail()
	}
	firstModule, err := store.Get(ctx, firstDigest)
	firstDocument := document(firstDigest)
	if err != nil || configurationStore.Activate(ctx, "runtime-generation-1", "2", models.Digest(firstDocument), firstDocument, map[string][]byte{"app": firstModule}) != nil {
		fail()
	}

	secondModule := append(append([]byte(nil), abiModule...), 0, 3, 1, 'x', 0)
	secondDigest := digest(secondModule)
	missingErr := readAndActivate(ctx, store, configurationStore, secondDigest, document(secondDigest))
	mismatchErr := configurationStore.Activate(ctx, "runtime-generation-2", "2", models.Digest(document(secondDigest)), document(secondDigest), map[string][]byte{"app": firstModule})
	before := snapshotView(configurationStore, "submit")
	if err := store.Put(ctx, secondDigest, bytes.NewReader(secondModule)); err != nil {
		fail()
	}
	artifactPath := filepath.Join(store.Root, secondDigest+".wasm")
	//nolint:gosec // G703: computed SHA-256 filename in the fixture temporary store; deliberately corrupts an artifact.
	if err := os.Remove(artifactPath); err != nil {
		fail()
	}
	//nolint:gosec // G703: computed SHA-256 filename in the fixture temporary store; deliberately corrupts an artifact.
	if err := os.WriteFile(artifactPath, []byte("tampered"), 0600); err != nil {
		fail()
	}
	_, tamperedErr := store.Get(ctx, secondDigest)
	//nolint:gosec // G703: computed SHA-256 filename in the fixture temporary store; deliberately corrupts an artifact.
	if err := os.Remove(artifactPath); err != nil {
		fail()
	}
	if err := store.Put(ctx, secondDigest, bytes.NewReader(secondModule)); err != nil {
		fail()
	}
	secondBytes, err := store.Get(ctx, secondDigest)
	if err != nil || configurationStore.Activate(ctx, "runtime-generation-2", "2", models.Digest(document(secondDigest)), document(secondDigest), map[string][]byte{"app": secondBytes}) != nil {
		fail()
	}
	after := snapshotView(configurationStore, "submit")
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{
		"missingArtifactRejected":        missingErr != nil,
		"mismatchedArtifactRejected":     mismatchErr != nil,
		"tamperedArtifactRejected":       tamperedErr != nil,
		"activePreservedBeforeCandidate": before,
		"candidateArtifactAccepted":      true,
		"activePairPromotedTogether":     after,
	}); err != nil {
		os.Exit(1)
	}
}

func readAndActivate(ctx context.Context, store artifacts.Store, active *runtimeconfig.ConfigurationStore, moduleDigest string, raw []byte) error {
	module, err := store.Get(ctx, moduleDigest)
	if err != nil {
		return err
	}
	defer clear(module)
	return active.Activate(ctx, "runtime-generation-2", "2", models.Digest(raw), raw, map[string][]byte{"app": module})
}

func snapshotView(store *runtimeconfig.ConfigurationStore, commandName string) map[string]any {
	var result map[string]any
	found, err := store.WithCommand(commandName, func(view runtimeconfig.CommandView) error {
		actual := sha256.Sum256(view.Module)
		result = map[string]any{
			"generation":             view.Generation,
			"moduleId":               view.Command.ModuleID,
			"moduleDigest":           view.ModuleDigest,
			"moduleBytesMatchDigest": hex.EncodeToString(actual[:]) == view.ModuleDigest,
		}
		return nil
	})
	if err != nil || !found {
		fail()
	}
	return result
}

func document(moduleDigest string) []byte {
	return []byte(fmt.Sprintf(`{"schemaVersion":"2","modules":[{"moduleId":"app","sha256":%q}],"commands":[{"name":"submit","moduleId":"app","export":"invoke","entities":["entries"]}]}`, moduleDigest))
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func mustDecode(value string) []byte {
	result, err := hex.DecodeString(value)
	if err != nil {
		panic(err)
	}
	return result
}

func fail() { os.Exit(1) }
