package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/Liapoldus/plugin-sdk/domain/models"
	"github.com/Liapoldus/runtime/contracts"
	runtimeconfig "github.com/Liapoldus/runtime/internal/application/config"
	"github.com/Liapoldus/runtime/internal/infrastructure/wasm"
)

// The module starts with one 64-KiB page, attempts to grow by 1024 pages
// against the contract maximum of 1024 total pages, then accesses page 2.
var growthWasm = mustDecode("0061736d01000000010c0260017f017f60027f7f017e03030200010503010001071b03066d656d6f7279020005616c6c6f63000006696e766f6b6500010a1902040041000b120041800840001a418080042d00001a42000b")

func main() {
	ctx := context.Background()
	moduleDigest := digest(growthWasm)
	document := []byte(fmt.Sprintf(`{"schemaVersion":"1","groupId":"memoryLimit","modelInstanceId":"domain","module":{"sha256":%q},"commands":[{"name":"attempt","export":"invoke","tenantSites":["tenant/site"],"entities":["records"]}]}`, moduleDigest))
	active := &runtimeconfig.ConfigurationStore{}
	if err := wasm.ValidateModule(ctx, growthWasm); err != nil {
		fail()
	}
	if err := active.Activate(ctx, "memory-limit-baseline", "1", models.Digest(document), document, growthWasm); err != nil {
		fail()
	}
	before, ok := active.Snapshot()
	if !ok {
		fail()
	}
	_, executionErr := (wasm.Executor{}).Execute(ctx, before.Module, []byte(`{"attempt":true}`))
	after, ok := active.Snapshot()
	if !ok {
		fail()
	}
	abi := contracts.WasmABIDefinition()
	errorCode := ""
	if errors.Is(executionErr, wasm.ErrExecutionFailed) {
		errorCode = "execution_failed"
	}
	activeUnchanged := before.Generation == after.Generation && before.SHA256 == after.SHA256 &&
		before.Settings.GroupID == after.Settings.GroupID && bytes.Equal(before.Module, after.Module)
	if json.NewEncoder(os.Stdout).Encode(map[string]any{
		"error":                        errorCode,
		"attemptedGrowthPages":         1024,
		"configuredMaximumPages":       abi.Limits.MaxMemoryPages,
		"activeConfigurationPreserved": activeUnchanged,
		"activeGeneration":             after.Generation,
	}) != nil {
		fail()
	}
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func mustDecode(encoded string) []byte {
	decoded, err := hex.DecodeString(encoded)
	if err != nil {
		panic(err)
	}
	return decoded
}

func fail() {
	if _, err := fmt.Fprintln(os.Stderr, "WASM memory boundary scenario failed"); err != nil {
		os.Exit(1)
	}
	os.Exit(1)
}
