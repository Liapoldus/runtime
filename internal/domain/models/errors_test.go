package models_test

import (
	"errors"
	"fmt"
	models "github.com/Liapoldus/runtime/internal/domain/models"
	"testing"
)

func TestInternalErrorTokensAndSentinelIdentity(t *testing.T) {
	for _, test := range []struct {
		err   error
		token string
	}{
		{models.ErrInvalidArtifactDigest, "invalid module artifact digest"},
		{models.ErrArtifactDigestMismatch, "module artifact digest mismatch"},
		{models.ErrInvalidArtifactModule, "invalid module artifact"},
		{models.ErrArtifactTooLarge, "module artifact too large"},
	} {
		if test.err.Error() != test.token {
			t.Fatalf("error token changed: %q", test.err.Error())
		}
		if !errors.Is(fmt.Errorf("wrapped: %w", test.err), test.err) {
			t.Fatalf("sentinel identity changed: %s", test.token)
		}
	}
}
