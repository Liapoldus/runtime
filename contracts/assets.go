package contracts

import (
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"sync"
)

//go:embed v1/*.json
var assets embed.FS

var ErrInvalidAssets = errors.New("invalid product contract assets")

type WasmABI struct {
	Version int `json:"version"`
	Exports struct {
		Memory   string `json:"memory"`
		Allocate string `json:"allocate"`
		Invoke   string `json:"invoke"`
	} `json:"exports"`
	Limits struct {
		MaxInputBytes     int    `json:"maxInputBytes"`
		MaxOutputBytes    int    `json:"maxOutputBytes"`
		MaxModuleBytes    int    `json:"maxModuleBytes"`
		MaxMemoryPages    uint32 `json:"maxMemoryPages"`
		MaxDurationMillis int    `json:"maxDurationMillis"`
	} `json:"limits"`
}

var (
	abiOnce sync.Once
	abi     WasmABI
	abiErr  error
)

func LoadWasmABI() (WasmABI, error) {
	abiOnce.Do(func() {
		data, err := fs.ReadFile(assets, "v1/wasm-abi.json")
		if err != nil {
			abiErr = ErrInvalidAssets
			return
		}
		if err := json.Unmarshal(data, &abi); err != nil || abi.Version != 1 ||
			abi.Exports.Memory == "" || abi.Exports.Allocate == "" || abi.Exports.Invoke == "" ||
			abi.Limits.MaxInputBytes <= 0 || abi.Limits.MaxOutputBytes <= 0 ||
			abi.Limits.MaxModuleBytes <= 0 || abi.Limits.MaxMemoryPages == 0 ||
			abi.Limits.MaxDurationMillis <= 0 {
			abiErr = ErrInvalidAssets
		}
	})
	return abi, abiErr
}
