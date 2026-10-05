package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/Liapoldus/runtime/internal/infrastructure/wasm"
)

func main() {
	var request struct {
		WASM  []byte          `json:"wasm"`
		Input json.RawMessage `json:"input"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
		os.Exit(2)
	}
	output, err := (wasm.Executor{}).Execute(context.Background(), request.WASM, request.Input)
	response := struct {
		Output json.RawMessage `json:"output,omitempty"`
		Error  string          `json:"error,omitempty"`
	}{Output: output}
	if err != nil {
		switch {
		case errors.Is(err, wasm.ErrInvalidABI):
			response.Error = "invalid_abi"
		case errors.Is(err, wasm.ErrTooLarge):
			response.Error = "payload_too_large"
		case errors.Is(err, wasm.ErrInvalidJSON):
			response.Error = "invalid_json"
		case errors.Is(err, wasm.ErrInvalidModule):
			response.Error = "invalid_module"
		default:
			response.Error = "execution_failed"
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(response); err != nil {
		os.Exit(2)
	}
}
