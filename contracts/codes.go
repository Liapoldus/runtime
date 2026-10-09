// Package contracts owns Runtime public schemas, metadata and sandbox definitions.
package contracts

// Error tokens preserve their published wire spelling.
const (
	InvokeCapability     string = "runtime.invoke"
	CodeNotReady         string = "not_ready"
	CodeUnknownCommand   string = "unknown_command"
	CodeForbiddenScope   string = "forbidden_scope"
	CodeForbiddenEntity  string = "forbidden_entity"
	CodeInvalidRequest   string = "invalid_request"
	CodePayloadTooLarge  string = "payload_too_large"
	CodeExecutionFailed  string = "execution_failed"
	CodeExecutionTimeout string = "execution_timeout"
	CodeCancelled        string = "cancelled"
	CodeInvalidModule    string = "invalid_module"
	CodeInvalidABI       string = "invalid_abi"
	CodeInvalidJSON      string = "invalid_json"
	CodeInvalidInput     string = "invalid_input"
	CodeDigestMismatch   string = "digest_mismatch"
	CodeArtifactTooLarge string = "artifact_too_large"
	CodeUnavailable      string = "unavailable"
)
