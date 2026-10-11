// Package config validates and atomically activates Runtime generations.
package config

// These definitions preserve the existing settings validator and its result tokens.
const (
	configurationSchemaVersion string = "2"
	configurationMaxCommands   int    = 128
	configurationMaxModules    int    = 64
	commandMaxEntities         int    = 128
	commandMaxCalls            int    = 64
	identifierMaxBytes         int    = 64
	moduleDigestHexBytes       int    = 64
	codeInvalidConfiguration   string = "invalid_configuration"
	codeInvalidDigest          string = "invalid_digest"
	codeInvalidCommand         string = "invalid_command"
	codeDuplicateCommand       string = "duplicate_command"
)
