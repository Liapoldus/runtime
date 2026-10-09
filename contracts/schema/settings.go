// Package schema owns the public Runtime JSON schema definitions.
package schema

// Settings returns a fresh public JSON schema definition.
func Settings() map[string]any {
	return map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  "https://liapoldus.github.io/spec/runtime/v1/settings.schema.json",
		"title":                "Runtime v1 settings",
		"type":                 "object",
		"additionalProperties": false,
		"required": []any{
			"schemaVersion",
			"groupId",
			"modelInstanceId",
			"module",
			"commands",
		},
		"properties": map[string]any{
			"schemaVersion": map[string]any{
				"const": "1",
			},
			"groupId": map[string]any{
				"$ref": "#/$defs/identifier",
			},
			"modelInstanceId": map[string]any{
				"$ref": "#/$defs/identifier",
			},
			"module": map[string]any{
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
			},
			"commands": map[string]any{
				"type":     "array",
				"minItems": 1,
				"maxItems": 128,
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required": []any{
						"name",
						"export",
						"tenantSites",
						"entities",
					},
					"properties": map[string]any{
						"name": map[string]any{
							"$ref": "#/$defs/identifier",
						},
						"export": map[string]any{
							"$ref": "#/$defs/identifier",
						},
						"tenantSites": map[string]any{
							"type":        "array",
							"minItems":    1,
							"maxItems":    256,
							"uniqueItems": true,
							"items": map[string]any{
								"type":    "string",
								"pattern": "^[A-Za-z0-9_-]+/[A-Za-z0-9_-]+$",
							},
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
					},
				},
			},
		},
		"$defs": map[string]any{
			"identifier": map[string]any{
				"type":    "string",
				"pattern": "^[A-Za-z][A-Za-z0-9_]{0,63}$",
			},
		},
	}
}
