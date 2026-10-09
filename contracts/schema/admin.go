package schema

// Actions returns a fresh public JSON schema definition.
func Actions() map[string]any {
	return map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id":     "https://liapoldus.github.io/plugins/runtime/admin-actions/v1/schema.json",
		"title":   "Runtime plugin Admin Actions v1",
		"type":    "object",
		"required": []any{
			"runtime.modules.list",
			"runtime.modules.publish",
		},
		"additionalProperties": false,
		"properties": map[string]any{
			"runtime.modules.list": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required": []any{
					"kind",
					"ownership",
					"surfaceBinding",
					"requestSchema",
					"responseSchema",
					"httpStatus",
					"errors",
				},
				"properties": map[string]any{
					"kind": map[string]any{
						"const": "query",
					},
					"ownership": map[string]any{
						"const": "plugin-admin-surface-capability",
					},
					"surfaceBinding": map[string]any{
						"type":                 "object",
						"additionalProperties": false,
						"required": []any{
							"page",
							"section",
						},
						"properties": map[string]any{
							"page": map[string]any{
								"const": "modules",
							},
							"section": map[string]any{
								"const": "modules",
							},
						},
					},
					"requestSchema": map[string]any{
						"type": "object",
					},
					"responseSchema": map[string]any{
						"type": "object",
					},
					"httpStatus": map[string]any{
						"const": 200,
					},
					"errors": map[string]any{
						"$ref": "#/$defs/errors",
					},
				},
			},
			"runtime.modules.publish": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required": []any{
					"kind",
					"ownership",
					"surfaceBinding",
					"transport",
					"multipart",
					"acceptedHttpStatus",
					"receiptSchema",
					"archiveLimits",
					"metadataSchema",
					"idempotency",
					"errors",
				},
				"properties": map[string]any{
					"kind": map[string]any{
						"const": "artifact-action",
					},
					"ownership": map[string]any{
						"const": "plugin-admin-surface-action",
					},
					"surfaceBinding": map[string]any{
						"type":                 "object",
						"additionalProperties": false,
						"required": []any{
							"page",
							"section",
							"action",
						},
						"properties": map[string]any{
							"page": map[string]any{
								"const": "modules",
							},
							"section": map[string]any{
								"const": "modules",
							},
							"action": map[string]any{
								"const": "publish",
							},
						},
					},
					"transport": map[string]any{
						"const": "plugin-sdk.rest.artifact-stream",
					},
					"multipart": map[string]any{
						"type":                 "object",
						"additionalProperties": false,
						"required": []any{
							"parts",
							"order",
							"artifactFilenameForwarded",
						},
						"properties": map[string]any{
							"parts": map[string]any{
								"const": []any{
									"metadata",
									"artifact",
								},
							},
							"order": map[string]any{
								"const": []any{
									"metadata",
									"artifact",
								},
							},
							"artifactFilenameForwarded": map[string]any{
								"const": false,
							},
						},
					},
					"acceptedHttpStatus": map[string]any{
						"const": 202,
					},
					"receiptSchema": map[string]any{
						"const": "contracts/v1/module-artifact-receipt.schema.json",
					},
					"metadataSchema": map[string]any{
						"const": "contracts/v1/module-artifact-metadata.schema.json",
					},
					"archiveLimits": map[string]any{
						"type":                 "object",
						"additionalProperties": false,
						"required": []any{
							"mediaType",
							"minArtifactBytes",
							"artifactBytes",
							"metadataBytes",
							"multipartOverheadBytes",
						},
						"properties": map[string]any{
							"mediaType": map[string]any{
								"const": "application/wasm",
							},
							"minArtifactBytes": map[string]any{
								"const": 1,
							},
							"artifactBytes": map[string]any{
								"const": 16777216,
							},
							"metadataBytes": map[string]any{
								"const": 65536,
							},
							"multipartOverheadBytes": map[string]any{
								"const": 65536,
							},
						},
					},
					"idempotency": map[string]any{
						"type": "object",
					},
					"errors": map[string]any{
						"$ref": "#/$defs/errors",
					},
				},
			},
		},
		"$defs": map[string]any{
			"errors": map[string]any{
				"type":          "object",
				"minProperties": 1,
				"additionalProperties": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required": []any{
						"http",
						"code",
					},
					"properties": map[string]any{
						"http": map[string]any{
							"type":    "integer",
							"minimum": 400,
							"maximum": 599,
						},
						"code": map[string]any{
							"type":      "string",
							"minLength": 1,
							"maxLength": 128,
						},
					},
				},
			},
		},
	}
}

// Surface returns a fresh public JSON schema definition.
func Surface() map[string]any {
	return map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  "https://liapoldus.github.io/plugins/runtime/admin-ui/v1/schema.json",
		"title":                "Runtime plugin Admin Surface v1",
		"type":                 "object",
		"additionalProperties": false,
		"required": []any{
			"version",
			"plugin",
			"requiredCapabilities",
			"pages",
		},
		"properties": map[string]any{
			"version": map[string]any{
				"const": 1,
			},
			"plugin": map[string]any{
				"const": "runtime",
			},
			"requiredCapabilities": map[string]any{
				"type":        "array",
				"minItems":    1,
				"uniqueItems": true,
				"items": map[string]any{
					"type":    "string",
					"pattern": "^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$",
				},
			},
			"pages": map[string]any{
				"type":     "array",
				"minItems": 1,
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required": []any{
						"id",
						"title",
						"capability",
						"permissions",
						"sections",
					},
					"properties": map[string]any{
						"id": map[string]any{
							"const": "modules",
						},
						"title": map[string]any{
							"type":      "string",
							"minLength": 1,
						},
						"capability": map[string]any{
							"const": "runtime.modules.list",
						},
						"permissions": map[string]any{
							"type":        "array",
							"minItems":    1,
							"uniqueItems": true,
							"items": map[string]any{
								"type": "string",
							},
						},
						"sections": map[string]any{
							"type":     "array",
							"minItems": 1,
							"items": map[string]any{
								"type":                 "object",
								"additionalProperties": false,
								"required": []any{
									"id",
									"kind",
									"title",
									"dataCapability",
									"columns",
									"actions",
								},
								"properties": map[string]any{
									"id": map[string]any{
										"const": "modules",
									},
									"kind": map[string]any{
										"const": "table",
									},
									"title": map[string]any{
										"type":      "string",
										"minLength": 1,
									},
									"dataCapability": map[string]any{
										"const": "runtime.modules.list",
									},
									"columns": map[string]any{
										"const": []any{
											"sha256",
											"sizeBytes",
										},
									},
									"actions": map[string]any{
										"type":     "array",
										"minItems": 1,
										"items": map[string]any{
											"type":                 "object",
											"additionalProperties": false,
											"required": []any{
												"id",
												"title",
												"capability",
												"confirmation",
												"inputSchema",
												"artifactInput",
											},
											"properties": map[string]any{
												"id": map[string]any{
													"const": "publish",
												},
												"title": map[string]any{
													"type":      "string",
													"minLength": 1,
												},
												"capability": map[string]any{
													"const": "runtime.modules.publish",
												},
												"confirmation": map[string]any{
													"type":      "string",
													"minLength": 1,
												},
												"inputSchema": map[string]any{
													"type":                 "object",
													"additionalProperties": false,
													"required": []any{
														"type",
														"properties",
														"required",
														"additionalProperties",
													},
													"properties": map[string]any{
														"type": map[string]any{
															"const": "object",
														},
														"properties": map[string]any{
															"type": "object",
															"additionalProperties": map[string]any{
																"type":                 "object",
																"additionalProperties": false,
																"required": []any{
																	"type",
																},
																"properties": map[string]any{
																	"type": map[string]any{
																		"const": "string",
																	},
																	"minLength": map[string]any{
																		"type":    "integer",
																		"minimum": 0,
																	},
																	"maxLength": map[string]any{
																		"type":    "integer",
																		"minimum": 0,
																	},
																},
															},
														},
														"required": map[string]any{
															"type":        "array",
															"uniqueItems": true,
															"items": map[string]any{
																"type": "string",
															},
														},
														"additionalProperties": map[string]any{
															"const": false,
														},
													},
												},
												"artifactInput": map[string]any{
													"type":                 "object",
													"additionalProperties": false,
													"required": []any{
														"mediaTypes",
														"maxBytes",
														"maxMetadataBytes",
														"maxMultipartOverheadBytes",
													},
													"properties": map[string]any{
														"mediaTypes": map[string]any{
															"const": []any{
																"application/wasm",
															},
														},
														"maxBytes": map[string]any{
															"const": 16777216,
														},
														"maxMetadataBytes": map[string]any{
															"const": 65536,
														},
														"maxMultipartOverheadBytes": map[string]any{
															"const": 65536,
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}
