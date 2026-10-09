package config_test

import (
	"errors"
	"fmt"
	application "github.com/Liapoldus/runtime/internal/application/config"
	"testing"
)

func TestInternalErrorTokensAndSentinelIdentity(t *testing.T) {
	for _, test := range []struct {
		err   error
		token string
	}{
		{application.ErrConfigurationRejected, "Runtime configuration rejected"},
	} {
		if test.err.Error() != test.token {
			t.Fatalf("error token changed: %q", test.err.Error())
		}
		if !errors.Is(fmt.Errorf("wrapped: %w", test.err), test.err) {
			t.Fatalf("sentinel identity changed: %s", test.token)
		}
	}
}
