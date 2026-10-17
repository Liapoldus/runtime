package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/Liapoldus/service-sdk/v2/domain/models"
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
	document := []byte(fmt.Sprintf(`{"schemaVersion":"2","modules":[{"moduleId":"app","sha256":%q}],"commands":[{"name":"attempt","moduleId":"app","export":"invoke","entities":["records"]}]}`, moduleDigest))
	active := &runtimeconfig.ConfigurationStore{}
	if err := wasm.ValidateModule(ctx, growthWasm); err != nil {
		fail()
	}
	if err := active.Activate(ctx, "memory-limit-baseline", "2", models.Digest(document), document, map[string][]byte{"app": growthWasm}); err != nil {
		fail()
	}
	before := activeView(active)
	_, executionErr := (wasm.Executor{}).ExecuteWithHostExport(ctx, growthWasm, "invoke", []byte(`{"attempt":true}`), nil)
	after := activeView(active)
	abi := contracts.WasmABIDefinition()
	errorCode := ""
	if errors.Is(executionErr, wasm.ErrExecutionFailed) {
		errorCode = "execution_failed"
	}
	activeUnchanged := before == after
	if json.NewEncoder(os.Stdout).Encode(map[string]any{
		"error":                        errorCode,
		"attemptedGrowthPages":         1024,
		"configuredMaximumPages":       abi.Limits.MaxMemoryPages,
		"activeConfigurationPreserved": activeUnchanged,
		"activeGeneration":             after.generation,
	}) != nil {
		fail()
	}
}

type configView struct {
	generation string
	settings   string
	module     string
}

func activeView(store *runtimeconfig.ConfigurationStore) configView {
	var result configView
	found, err := store.WithCommand("attempt", func(command runtimeconfig.CommandView) error {
		module := sha256.Sum256(command.Module)
		result = configView{
			generation: command.Generation,
			settings:   command.SHA256,
			module:     fmt.Sprintf("%s:%s", command.ModuleDigest, hex.EncodeToString(module[:])),
		}
		return nil
	})
	if err != nil || !found {
		fail()
	}
	return result
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
