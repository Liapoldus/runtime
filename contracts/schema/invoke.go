package schema

// Request returns a fresh public JSON schema definition.
func Request() map[string]any {
	return map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  "https://liapoldus.github.io/spec/runtime/v1/invoke-request.schema.json",
		"title":                "Runtime v1 invoke request",
		"description":          "Body of the runtime.invoke peer method. The host validates the declared scope and entities against the active command allowlist before any WASM execution.",
		"type":                 "object",
		"additionalProperties": false,
		"required": []any{
			"command",
			"scope",
			"input",
		},
		"properties": map[string]any{
			"command": map[string]any{
				"$ref": "#/$defs/identifier",
			},
			"scope": map[string]any{
				"type":    "string",
				"pattern": "^[A-Za-z0-9_-]+/[A-Za-z0-9_-]+$",
			},
			"entities": map[string]any{
				"type":        "array",
				"minItems":    1,
				"maxItems":    128,
				"uniqueItems": true,
				"items": map[string]any{
					"$ref": "#/$defs/identifier",
				},
			},
			"input": map[string]any{},
		},
		"$defs": map[string]any{
			"identifier": map[string]any{
				"type":    "string",
				"pattern": "^[A-Za-z][A-Za-z0-9_]{0,63}$",
			},
		},
	}
}

// Success returns a fresh public JSON schema definition.
func Success() map[string]any {
	return map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  "https://liapoldus.github.io/spec/runtime/v1/invoke-success.schema.json",
		"title":                "Runtime v1 invoke success body",
		"description":          "Content of the body string returned for a successful runtime.invoke call, wrapped by the transport in the advertised response action.",
		"type":                 "object",
		"additionalProperties": false,
		"required": []any{
			"command",
			"output",
		},
		"properties": map[string]any{
			"command": map[string]any{
				"type":    "string",
				"pattern": "^[A-Za-z][A-Za-z0-9_]{0,63}$",
			},
			"output": map[string]any{},
		},
	}
}
