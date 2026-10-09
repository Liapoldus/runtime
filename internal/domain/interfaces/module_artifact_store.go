// Package interfaces defines Runtime domain ports.
package interfaces

import (
	"context"
	"io"

	"github.com/Liapoldus/runtime/internal/domain/models"
)

type ModuleArtifactStore interface {
	Put(ctx context.Context, expectedDigest string, source io.Reader) error
	List(ctx context.Context) ([]models.ModuleArtifact, error)
}
