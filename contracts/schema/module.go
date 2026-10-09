package schema

// Metadata returns a fresh public JSON schema definition.
func Metadata() map[string]any {
	return map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  "https://liapoldus.github.io/plugins/runtime/v1/module-artifact-metadata.schema.json",
		"title":                "Runtime module upload metadata v1",
		"type":                 "object",
		"additionalProperties": false,
		"required": []any{
			"sha256",
		},
		"properties": map[string]any{
			"sha256": map[string]any{
				"type":    "string",
				"pattern": "^[a-f0-9]{64}$",
			},
		},
	}
}

// Receipt returns a fresh public JSON schema definition.
func Receipt() map[string]any {
	return map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  "https://liapoldus.github.io/plugins/runtime/v1/module-artifact-receipt.schema.json",
		"title":                "Runtime module upload receipt v1",
		"type":                 "object",
		"additionalProperties": false,
		"required": []any{
			"sha256",
		},
		"properties": map[string]any{
			"sha256": map[string]any{
				"type":    "string",
				"pattern": "^[a-f0-9]{64}$",
			},
		},
	}
}
