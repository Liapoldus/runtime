// Package artifacts stores validated content-addressed WASM modules.
package artifacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Liapoldus/runtime/contracts"
	"github.com/Liapoldus/runtime/internal/domain/models"
	"github.com/Liapoldus/runtime/internal/infrastructure/wasm"
)

// Store accepts a bounded immutable WASM artifact. The module is compiled with
// no host imports, WASI, filesystem or network before it becomes visible.
type Store struct {
	Root string
}

func (store Store) Put(ctx context.Context, expected string, source io.Reader) (resultErr error) {
	abi := contracts.WasmABIDefinition()
	if len(expected) != 64 || strings.ToLower(expected) != expected {
		return models.ErrInvalidArtifactDigest
	}
	if _, err := hex.DecodeString(expected); err != nil {
		return models.ErrInvalidArtifactDigest
	}
	if err := os.MkdirAll(store.Root, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(store.Root, ".upload-*")
	if err != nil {
		return err
	}
	defer func() {
		err := os.Remove(file.Name())
		if !errors.Is(err, os.ErrNotExist) {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	defer func() {
		err := file.Close()
		if !errors.Is(err, os.ErrClosed) {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(source, int64(abi.Limits.MaxModuleBytes)+1))
	if err != nil {
		return err
	}
	if written > int64(abi.Limits.MaxModuleBytes) {
		return models.ErrArtifactTooLarge
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected {
		return models.ErrArtifactDigestMismatch
	}
	module, err := os.ReadFile(file.Name())
	if err != nil {
		return err
	}
	if err := wasm.ValidateModule(ctx, module); err != nil {
		return models.ErrInvalidArtifactModule
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Chmod(0400); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	finalPath := filepath.Join(store.Root, expected+".wasm")
	if err := os.Rename(file.Name(), finalPath); err != nil {
		return err
	}
	directory, err := os.Open(store.Root)
	if err != nil {
		return err
	}
	defer func() {
		err := directory.Close()
		if !errors.Is(err, os.ErrClosed) {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	return directory.Sync()
}

// Get returns the immutable module only when its path, bytes and Runtime ABI
// still match the requested content digest. It is bounded by the published ABI
// limit even if the artifact directory was modified outside Store.Put.
func (store Store) Get(ctx context.Context, expected string) (resultBytes []byte, resultErr error) {
	abi := contracts.WasmABIDefinition()
	if !validDigest(expected) || store.Root == "" || ctx == nil || ctx.Err() != nil {
		return nil, models.ErrInvalidArtifactDigest
	}
	path := filepath.Join(store.Root, expected+".wasm")
	metadata, err := os.Lstat(path)
	if err != nil || !metadata.Mode().IsRegular() || metadata.Size() <= 0 || metadata.Size() > int64(abi.Limits.MaxModuleBytes) {
		return nil, models.ErrInvalidArtifactModule
	}
	//nolint:gosec // G304: the basename is a validated lowercase SHA-256 digest plus .wasm.
	file, err := os.Open(path)
	if err != nil {
		return nil, models.ErrInvalidArtifactModule
	}
	defer func() {
		err := file.Close()
		if !errors.Is(err, os.ErrClosed) {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	module, err := io.ReadAll(io.LimitReader(file, int64(abi.Limits.MaxModuleBytes)+1))
	if err != nil || int64(len(module)) > int64(abi.Limits.MaxModuleBytes) || ctx.Err() != nil {
		clear(module)
		return nil, models.ErrInvalidArtifactModule
	}
	actual := sha256.Sum256(module)
	if hex.EncodeToString(actual[:]) != expected {
		clear(module)
		return nil, models.ErrArtifactDigestMismatch
	}
	if err := wasm.ValidateModule(ctx, module); err != nil {
		clear(module)
		return nil, models.ErrInvalidArtifactModule
	}
	return module, nil
}

func (store Store) List(ctx context.Context) ([]models.ModuleArtifact, error) {
	if ctx == nil || ctx.Err() != nil || store.Root == "" {
		return nil, models.ErrInvalidArtifactModule
	}
	entries, err := os.ReadDir(store.Root)
	if errors.Is(err, os.ErrNotExist) {
		return []models.ModuleArtifact{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := make([]models.ModuleArtifact, 0, len(entries))
	for _, entry := range entries {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".wasm") {
			continue
		}
		digest := strings.TrimSuffix(name, ".wasm")
		if !validDigest(digest) {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
			continue
		}
		module, err := store.Get(ctx, digest)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		clear(module)
		result = append(result, models.ModuleArtifact{SHA256: digest, SizeBytes: info.Size()})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].SHA256 < result[right].SHA256 })
	return result, nil
}

func validDigest(expected string) bool {
	if len(expected) != 64 || strings.ToLower(expected) != expected {
		return false
	}
	_, err := hex.DecodeString(expected)
	return err == nil
}
