package artifacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Liapoldus/runtime/contracts"
	"github.com/tetratelabs/wazero"
)

var (
	ErrInvalidDigest  = errors.New("invalid_digest")
	ErrDigestMismatch = errors.New("digest_mismatch")
	ErrInvalidModule  = errors.New("invalid_module")
	ErrTooLarge       = errors.New("artifact_too_large")
)

// Store accepts a bounded immutable WASM artifact. The module is compiled with
// no host imports, WASI, filesystem or network before it becomes visible.
type Store struct {
	Root string
}

func (store Store) Put(ctx context.Context, expected string, source io.Reader) error {
	abi, err := contracts.LoadWasmABI()
	if err != nil {
		return ErrInvalidModule
	}
	if len(expected) != 64 || strings.ToLower(expected) != expected {
		return ErrInvalidDigest
	}
	if _, err := hex.DecodeString(expected); err != nil {
		return ErrInvalidDigest
	}
	if err := os.MkdirAll(store.Root, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(store.Root, ".upload-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(source, int64(abi.Limits.MaxModuleBytes)+1))
	if err != nil {
		return err
	}
	if written > int64(abi.Limits.MaxModuleBytes) {
		return ErrTooLarge
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected {
		return ErrDigestMismatch
	}
	module, err := os.ReadFile(file.Name())
	if err != nil {
		return err
	}
	runtime := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfigInterpreter().WithMemoryLimitPages(abi.Limits.MaxMemoryPages).WithCloseOnContextDone(true))
	defer runtime.Close(ctx)
	compiled, err := runtime.CompileModule(ctx, module)
	if err != nil {
		return ErrInvalidModule
	}
	compiled.Close(ctx)
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
	defer directory.Close()
	return directory.Sync()
}
