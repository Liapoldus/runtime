package models

import "errors"

type Module struct {
	SHA256 string `json:"sha256"`
}

type ModuleArtifact struct {
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"sizeBytes"`
}

const (
	messageInvalidArtifactDigest  string = "invalid module artifact digest"
	messageArtifactDigestMismatch string = "module artifact digest mismatch"
	messageInvalidArtifactModule  string = "invalid module artifact"
	messageArtifactTooLarge       string = "module artifact too large"
)

var (
	ErrInvalidArtifactDigest  = errors.New(messageInvalidArtifactDigest)
	ErrArtifactDigestMismatch = errors.New(messageArtifactDigestMismatch)
	ErrInvalidArtifactModule  = errors.New(messageInvalidArtifactModule)
	ErrArtifactTooLarge       = errors.New(messageArtifactTooLarge)
)
