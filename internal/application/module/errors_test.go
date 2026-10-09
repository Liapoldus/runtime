package module_test

import (
	"errors"
	"fmt"
	application "github.com/Liapoldus/runtime/internal/application/module"
	"github.com/Liapoldus/runtime/internal/infrastructure/wasm"
	"testing"
)

func TestInternalErrorTokensAndSentinelIdentity(t *testing.T) {
	if errors.Is(application.ErrInvokeExecutionFailed, wasm.ErrExecutionFailed) {
		t.Fatal("layer-specific sentinels must remain distinct")
	}
	for _, test := range []struct {
		err   error
		token string
	}{
		{application.ErrInvokeNotReady, "not_ready"},
		{application.ErrInvokeUnknownCommand, "unknown_command"},
		{application.ErrInvokeForbiddenScope, "forbidden_scope"},
		{application.ErrInvokeForbiddenEntity, "forbidden_entity"},
		{application.ErrInvokeInvalidRequest, "invalid_request"},
		{application.ErrInvokePayloadTooLarge, "payload_too_large"},
		{application.ErrInvokeExecutionFailed, "execution_failed"},
		{application.ErrInvokeExecutionTimeout, "execution_timeout"},
		{application.ErrInvokeCancelled, "cancelled"},
	} {
		if test.err.Error() != test.token {
			t.Fatalf("error token changed: %q", test.err.Error())
		}
		if !errors.Is(fmt.Errorf("wrapped: %w", test.err), test.err) {
			t.Fatalf("sentinel identity changed: %s", test.token)
		}
	}
}
