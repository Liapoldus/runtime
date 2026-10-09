package wasm_test

import (
	"errors"
	"fmt"
	wasm "github.com/Liapoldus/runtime/internal/infrastructure/wasm"
	"testing"
)

func TestInternalErrorTokensAndSentinelIdentity(t *testing.T) {
	for _, test := range []struct {
		err   error
		token string
	}{
		{wasm.ErrInvalidModule, "invalid_module"},
		{wasm.ErrInvalidABI, "invalid_abi"},
		{wasm.ErrInvalidJSON, "invalid_json"},
		{wasm.ErrTooLarge, "payload_too_large"},
		{wasm.ErrExecutionFailed, "execution_failed"},
		{wasm.ErrExecutionTimeout, "execution_timeout"},
	} {
		if test.err.Error() != test.token {
			t.Fatalf("error token changed: %q", test.err.Error())
		}
		if !errors.Is(fmt.Errorf("wrapped: %w", test.err), test.err) {
			t.Fatalf("sentinel identity changed: %s", test.token)
		}
	}
}
