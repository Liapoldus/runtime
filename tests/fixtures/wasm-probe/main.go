package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/Liapoldus/runtime/internal/infrastructure/wasm"
)

func main() {
	var request struct {
		WASM              []byte          `json:"wasm"`
		Input             json.RawMessage `json:"input"`
		Cancel            bool            `json:"cancel"`
		CancelAfterMillis int             `json:"cancelAfterMillis"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
		os.Exit(2)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if request.Cancel {
		cancel()
	}
	if request.CancelAfterMillis > 0 {
		time.AfterFunc(time.Duration(request.CancelAfterMillis)*time.Millisecond, cancel)
	}
	output, err := (wasm.Executor{}).Execute(ctx, request.WASM, request.Input)
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
		case errors.Is(err, wasm.ErrExecutionTimeout):
			response.Error = "execution_timeout"
		case errors.Is(err, context.Canceled):
			response.Error = "cancelled"
		default:
			response.Error = "execution_failed"
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(response); err != nil {
		os.Exit(2)
	}
}
