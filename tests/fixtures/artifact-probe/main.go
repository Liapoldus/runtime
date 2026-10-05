package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/Liapoldus/runtime/internal/infrastructure/artifacts"
)

func main() {
	if len(os.Args) != 3 {
		os.Exit(2)
	}
	err := (artifacts.Store{Root: os.Args[1]}).Put(context.Background(), os.Args[2], os.Stdin)
	response := struct {
		Accepted bool   `json:"accepted"`
		Digest   string `json:"digest,omitempty"`
		Error    string `json:"error,omitempty"`
	}{Accepted: err == nil}
	if err == nil {
		response.Digest = os.Args[2]
	} else {
		switch {
		case errors.Is(err, artifacts.ErrInvalidDigest):
			response.Error = "invalid_digest"
		case errors.Is(err, artifacts.ErrDigestMismatch):
			response.Error = "digest_mismatch"
		case errors.Is(err, artifacts.ErrInvalidModule):
			response.Error = "invalid_module"
		case errors.Is(err, artifacts.ErrTooLarge):
			response.Error = "artifact_too_large"
		default:
			response.Error = "store_failed"
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(response); err != nil {
		os.Exit(2)
	}
}
