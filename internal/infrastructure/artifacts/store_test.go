package artifacts_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/Liapoldus/runtime/internal/domain/models"
	"github.com/Liapoldus/runtime/internal/infrastructure/artifacts"
	"os"
	"path/filepath"
	"testing"
)

func TestArtifactAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, hex string
		want      error
	}{
		{"minimalWasm", "0061736d01000000", models.ErrInvalidArtifactModule},
		{"abiWasm", "0061736d01000000010c0260017f017f60027f7f017e03030200010503010001071b03066d656d6f7279020005616c6c6f63000006696e766f6b6500010a1302040041000b0c002001ad4220862000ad840b", nil},
		{"wrongSignatureWasm", "0061736d01000000010d026000017f60027f7f017e03030200010503010001071b03066d656d6f7279020005616c6c6f63000006696e766f6b6500010a0b02040041000b040042000b", models.ErrInvalidArtifactModule},
		{"hostImportWasm", "0061736d01000000010401600000020d0103656e7605636c6f636b0000", models.ErrInvalidArtifactModule},
		{"globalImportWasm", "0061736d01000000010c0260017f017f60027f7f017e020b0104686f73740167037f0003030200010503010001071b03066d656d6f7279020005616c6c6f63000006696e766f6b6500010a1302040041000b0c002001ad4220862000ad840b", models.ErrInvalidArtifactModule},
		{"non-WASM", "7b226e6f74223a227761736d227d", models.ErrInvalidArtifactModule},
	} {
		t.Run(tc.name, func(t *testing.T) {
			module, err := hex.DecodeString(tc.hex)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(module)
			digest := hex.EncodeToString(sum[:])
			store := artifacts.Store{Root: t.TempDir()}
			if err := store.Put(t.Context(), digest, bytes.NewReader(module)); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			items, err := store.List(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if tc.want != nil {
				if len(items) != 0 {
					t.Fatal("rejected artifact was published")
				}
				return
			}
			actual, err := store.Get(t.Context(), digest)
			if err != nil || !bytes.Equal(actual, module) {
				t.Fatal("stored artifact changed")
			}
			if err := store.Put(t.Context(), digest, bytes.NewReader(module)); err != nil {
				t.Fatal(err)
			}
			items, err = store.List(t.Context())
			if err != nil || len(items) != 1 {
				t.Fatal("repeat publication changed catalog")
			}
		})
	}
	module, err := hex.DecodeString("0061736d01000000")
	if err != nil {
		t.Fatal(err)
	}
	store := artifacts.Store{Root: t.TempDir()}
	if err := store.Put(t.Context(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", bytes.NewReader(module)); !errors.Is(err, models.ErrArtifactDigestMismatch) {
		t.Fatal("digest mismatch accepted")
	}
	files, err := os.ReadDir(store.Root)
	if err != nil || len(files) != 0 {
		t.Fatal("rejected upload left files")
	}
}
func TestCatalogRejectsTamperedArtifact(t *testing.T) {
	module, err := hex.DecodeString("0061736d01000000010c0260017f017f60027f7f017e03030200010503010001071b03066d656d6f7279020005616c6c6f63000006696e766f6b6500010a1302040041000b0c002001ad4220862000ad840b")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(module)
	digest := hex.EncodeToString(sum[:])
	store := artifacts.Store{Root: t.TempDir()}
	if err := store.Put(t.Context(), digest, bytes.NewReader(module)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.Root, digest+".wasm")
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(t.Context(), digest); !errors.Is(err, models.ErrArtifactDigestMismatch) {
		t.Fatal("tampered artifact accepted")
	}
	catalog, err := store.List(t.Context())
	if err != nil || len(catalog) != 0 {
		t.Fatal("tampered artifact listed")
	}
}
