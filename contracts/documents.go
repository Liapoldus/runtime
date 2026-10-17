package contracts

import (
	"encoding/json"
	"fmt"

	sdkmodels "github.com/Liapoldus/service-sdk/v2/domain/models"
	"github.com/Liapoldus/runtime/contracts/schema"
)

// Documents constructs all public artifacts from fresh code-owned definitions.
func Documents() map[string]any {
	actions := ModuleAdminActionDefinitions()
	list := map[string]any{
		"ownership": "service-admin-surface-capability",
		"requestSchema": map[string]any{
			"$schema":              "https://json-schema.org/draft/2020-12/schema",
			"type":                 "object",
			"properties":           map[string]any{},
			"required":             []any{},
			"additionalProperties": false,
		},
		"responseSchema": map[string]any{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"type":    "object",
			"properties": map[string]any{
				"items": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"sha256": map[string]any{
								"type":    "string",
								"pattern": "^[a-f0-9]{64}$",
							},
							"sizeBytes": map[string]any{
								"type":    "integer",
								"minimum": 1,
							},
						},
						"required": []any{
							"sha256",
							"sizeBytes",
						},
						"additionalProperties": false,
					},
				},
				"nextCursor": map[string]any{
					"type": "null",
				},
			},
			"required": []any{
				"items",
				"nextCursor",
			},
			"additionalProperties": false,
		},
	}
	publish := map[string]any{
		"ownership": "service-admin-surface-action",
		"transport": "service-sdk.rest.artifact-stream",
		"multipart": map[string]any{
			"parts": []any{
				"metadata",
				"artifact",
			},
			"order": []any{
				"metadata",
				"artifact",
			},
			"artifactFilenameForwarded": false,
		},
		"receiptSchema":  "contracts/v2/module-artifact-receipt.schema.json",
		"metadataSchema": "contracts/v2/module-artifact-metadata.schema.json",
		"idempotency": map[string]any{
			"required": true,
			"scope": []any{
				"service-instance",
				"capability",
				"idempotency-key",
			},
			"semantics": "content-addressed",
			"sameInput": "no-op",
		},
	}
	list["kind"] = actions.List.Kind
	list["surfaceBinding"] = actions.List.SurfaceBinding
	list["httpStatus"] = actions.List.HTTPStatus
	list["errors"] = actions.List.Errors
	publish["kind"] = actions.Publish.Kind
	publish["surfaceBinding"] = actions.Publish.SurfaceBinding
	publish["acceptedHttpStatus"] = actions.Publish.AcceptedHTTPStatus
	publish["errors"] = actions.Publish.Errors
	publish["archiveLimits"] = map[string]any{"minArtifactBytes": 1, "multipartOverheadBytes": 65536,
		"mediaType": actions.Publish.ArchiveLimits.MediaType, "artifactBytes": actions.Publish.ArchiveLimits.ArtifactBytes, "metadataBytes": actions.Publish.ArchiveLimits.MetadataBytes}
	return map[string]any{
		"settings.schema.json":                schema.Settings(),
		"wasm-abi.json":                       WasmABIDefinition(),
		"module-artifact-receipt.schema.json": schema.Receipt(),
		"errors-invoke.json":                  InvokeErrorDefinitions(),
		"admin-actions.schema.json":           schema.Actions(),
		"admin-surface.json": map[string]any{
			"version": 1,
			"service": "runtime",
			"requiredCapabilities": []any{
				"runtime.modules.list",
				"runtime.modules.publish",
			},
			"pages": []any{
				map[string]any{
					"id":         "modules",
					"title":      "WASM-модули",
					"capability": "runtime.modules.list",
					"permissions": []any{
						"services.runtime.modules.manage",
					},
					"sections": []any{
						map[string]any{
							"id":             "modules",
							"kind":           "table",
							"title":          "Неизменяемые модули",
							"dataCapability": "runtime.modules.list",
							"columns": []any{
								"sha256",
								"sizeBytes",
							},
							"actions": []any{
								map[string]any{
									"id":           "publish",
									"title":        "Загрузить WASM-модуль",
									"capability":   "runtime.modules.publish",
									"confirmation": "Проверить digest и ABI, затем сохранить модуль как неизменяемый артефакт?",
									"inputSchema": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"sha256": map[string]any{
												"type":      "string",
												"minLength": 64,
												"maxLength": 64,
											},
										},
										"required": []any{
											"sha256",
										},
										"additionalProperties": false,
									},
									"artifactInput": map[string]any{
										"mediaTypes": []any{
											"application/wasm",
										},
										"maxBytes":                  16777216,
										"maxMetadataBytes":          65536,
										"maxMultipartOverheadBytes": 65536,
									},
								},
							},
						},
					},
				},
			},
		},
		"admin-surface.schema.json": schema.Surface(),
		"manifest.json":                       runtimeManifestDescriptor(),
		"invoke-success.schema.json": schema.Success(),
		"wasm-errors.json": map[string]any{
			"version": 2,
			"errors": map[string]any{
				"execution_failed": map[string]any{
					"code":                "execution_failed",
					"maximumMessageBytes": 0,
				},
			},
		},
		"admin-actions.json":                   map[string]any{ModuleListCapability: list, ModulePublishCapability: publish},
		"invoke-request.schema.json":           schema.Request(),
		"module-artifact-metadata.schema.json": schema.Metadata(),
	}
}

// Document serializes one definition using the generator's canonical encoding.
func Document(name string) ([]byte, error) {
	value, ok := Documents()[name]
	if !ok {
		return nil, fmt.Errorf("unknown contract: %s", name)
	}
	if name == "manifest.json" {
		return sdkmodels.CanonicalManifest(runtimeManifestDescriptor())
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func RuntimeDocuments() (RuntimeMetadata, error) {
	manifest, err := Document("manifest.json")
	if err != nil {
		return RuntimeMetadata{}, err
	}
	schema, err := Document("settings.schema.json")
	if err != nil {
		return RuntimeMetadata{}, err
	}
	surface, err := Document("admin-surface.json")
	if err != nil {
		return RuntimeMetadata{}, err
	}
	return RuntimeMetadata{Manifest: manifest, ConfigurationSchema: schema, AdminSurface: surface}, nil
}

func runtimeManifestDescriptor() sdkmodels.ManifestDescriptor {
	return sdkmodels.ManifestDescriptor{
		ID: "runtime", Version: "2.0.0",
		Capabilities: []sdkmodels.CapabilityDescriptor{{ID: "runtime.invoke", Version: "2.0.0"}},
	}
}
