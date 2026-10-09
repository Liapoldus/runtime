// Package config validates and atomically activates Runtime generations.
package config

// These definitions preserve the existing settings validator and its result tokens.
const (
	configurationSchemaVersion string = "1"
	configurationMaxCommands   int    = 128
	commandMaxEntities         int    = 128
	commandMaxTenantSites      int    = 256
	identifierMaxBytes         int    = 64
	moduleDigestHexBytes       int    = 64
	codeInvalidConfiguration   string = "invalid_configuration"
	codeInvalidDigest          string = "invalid_digest"
	codeInvalidCommand         string = "invalid_command"
	codeMissingScope           string = "missing_scope"
	codeInvalidScope           string = "invalid_scope"
	codeDuplicateCommand       string = "duplicate_command"
)
