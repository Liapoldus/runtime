// Package module implements bounded module publication and command invocation.
package module

import (
	"context"
	"io"

	"github.com/Liapoldus/runtime/internal/domain/interfaces"
	"github.com/Liapoldus/runtime/internal/domain/models"
)

type ModuleArtifacts struct {
	store interfaces.ModuleArtifactStore
}

func NewModuleArtifacts(store interfaces.ModuleArtifactStore) ModuleArtifacts {
	return ModuleArtifacts{store: store}
}

func (service ModuleArtifacts) Publish(ctx context.Context, digest string, source io.Reader) error {
	if service.store == nil || ctx == nil || ctx.Err() != nil || source == nil {
		return models.ErrInvalidArtifactModule
	}
	return service.store.Put(ctx, digest, source)
}

func (service ModuleArtifacts) List(ctx context.Context) ([]models.ModuleArtifact, error) {
	if service.store == nil || ctx == nil || ctx.Err() != nil {
		return nil, models.ErrInvalidArtifactModule
	}
	return service.store.List(ctx)
}
