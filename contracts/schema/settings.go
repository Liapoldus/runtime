// Package schema owns the public Runtime JSON schema definitions.
package schema

// Settings returns a fresh public JSON schema definition.
func Settings() map[string]any {
	return map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  "https://liapoldus.github.io/spec/runtime/v2/settings.schema.json",
		"title":                "Runtime v2 settings",
		"type":                 "object",
		"additionalProperties": false,
		"required": []any{
			"schemaVersion",
			"modules",
			"commands",
		},
		"properties": map[string]any{
			"schemaVersion": map[string]any{
				"const": "2",
			},
			"modules": map[string]any{
				"type": "array", "minItems": 1, "maxItems": 64, "uniqueItems": true,
				"items": map[string]any{
					"type": "object", "additionalProperties": false,
					"required": []any{"moduleId", "sha256"},
					"properties": map[string]any{
						"moduleId": map[string]any{"$ref": "#/$defs/identifier"},
						"sha256":   map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$"},
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
						"moduleId",
						"export",
						"entities",
					},
					"properties": map[string]any{
						"name": map[string]any{
							"$ref": "#/$defs/identifier",
						},
						"export": map[string]any{
							"$ref": "#/$defs/identifier",
						},
						"moduleId": map[string]any{
							"$ref": "#/$defs/identifier",
						},
						"entities": map[string]any{
							"type": "array", "minItems": 1, "maxItems": 128, "uniqueItems": true,
							"items": map[string]any{"$ref": "#/$defs/identifier"},
						},
						"calls": map[string]any{
							"type": "array", "maxItems": 64,
							"items": map[string]any{
								"type": "object", "additionalProperties": false,
								"required": []any{"plugin", "method"},
								"properties": map[string]any{
									"plugin": map[string]any{"type": "string", "pattern": "^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$"},
								"method": map[string]any{
									"type": "string", "maxLength": 128,
									"pattern": "^[A-Za-z][A-Za-z0-9_]{0,63}\\.[A-Za-z][A-Za-z0-9_]{0,63}$",
								},
								},
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
