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

	"github.com/Liapoldus/plugin-sdk/domain/models"
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
	firstDocument := document("first", firstDigest)
	if err != nil || configurationStore.Activate(ctx, "runtime-generation-1", "1", models.Digest(firstDocument), firstDocument, firstModule) != nil {
		fail()
	}

	secondModule := append(append([]byte(nil), abiModule...), 0, 3, 1, 'x', 0)
	secondDigest := digest(secondModule)
	missingErr := readAndActivate(ctx, store, configurationStore, secondDigest, document("second", secondDigest))
	mismatchErr := configurationStore.Activate(ctx, "runtime-generation-2", "1", models.Digest(document("second", secondDigest)), document("second", secondDigest), firstModule)
	before, ok := configurationStore.Snapshot()
	if !ok {
		fail()
	}
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
	if err != nil || configurationStore.Activate(ctx, "runtime-generation-2", "1", models.Digest(document("second", secondDigest)), document("second", secondDigest), secondBytes) != nil {
		fail()
	}
	after, ok := configurationStore.Snapshot()
	if !ok {
		fail()
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{
		"missingArtifactRejected":        missingErr != nil,
		"mismatchedArtifactRejected":     mismatchErr != nil,
		"tamperedArtifactRejected":       tamperedErr != nil,
		"activePreservedBeforeCandidate": snapshotView(before),
		"candidateArtifactAccepted":      true,
		"activePairPromotedTogether":     snapshotView(after),
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
	return active.Activate(ctx, "runtime-generation-2", "1", models.Digest(raw), raw, module)
}

func snapshotView(snapshot runtimeconfig.ConfigurationSnapshot) map[string]any {
	actual := sha256.Sum256(snapshot.Module)
	return map[string]any{
		"generation":             snapshot.Generation,
		"groupId":                snapshot.Settings.GroupID,
		"moduleDigest":           snapshot.Settings.Module.SHA256,
		"moduleBytesMatchDigest": hex.EncodeToString(actual[:]) == snapshot.Settings.Module.SHA256,
	}
}

func document(groupID, moduleDigest string) []byte {
	return []byte(fmt.Sprintf(`{"schemaVersion":"1","groupId":%q,"modelInstanceId":"domain","module":{"sha256":%q},"commands":[{"name":"submit","export":"invoke","tenantSites":["tenant/site"],"entities":["entries"]}]}`, groupID, moduleDigest))
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
