import manifest from '../contracts/v1/plugin.json';
import wasmABI from '../contracts/v1/wasm-abi.json';
import adminSurface from '../contracts/v1/admin-surface.json';
import adminSurfaceSchema from '../contracts/v1/admin-surface.schema.json';
import adminActionsSchema from '../contracts/v1/admin-actions.schema.json';
import actions from '../contracts/v1/admin-actions.json';
import { describe, expect, it } from 'vitest';







describe('product contract ownership', () => {
  it('declares runtime.invoke as the single peer capability', () => {
    expect(manifest.capabilities).toEqual([
      { name: 'runtime.invoke', mode: 'call' },
    ]);
  });

  it('declares exact WebAssembly function signatures in the versioned ABI', () => {
    expect(wasmABI.signatures).toEqual({
      allocate: { parameters: ['i32'], results: ['i32'] },
      invoke: { parameters: ['i32', 'i32'], results: ['i64'] },
    });
  });

  it('publishes the generic artifact-stream action for immutable WASM modules', () => {
    expect(manifest.adminSurface).toMatchObject({
      version: 1,
      descriptor: 'contracts/v1/admin-surface.json',
      schema: 'contracts/v1/admin-surface.schema.json',
      actions: 'contracts/v1/admin-actions.json',
      actionsSchema: 'contracts/v1/admin-actions.schema.json',
    });
    expect(adminSurface).toMatchObject({
      version: 1,
      plugin: 'runtime',
      requiredCapabilities: ['runtime.modules.list', 'runtime.modules.publish'],
      pages: [{
        id: 'modules',
        sections: [{
          id: 'modules',
          actions: [{
            id: 'publish',
            capability: 'runtime.modules.publish',
            artifactInput: {
              mediaTypes: ['application/wasm'],
              maxBytes: 16777216,
            },
          }],
        }],
      }],
    });

    expect(actions['runtime.modules.publish']).toMatchObject({
      kind: 'artifact-action',
      transport: 'plugin-sdk.rest.artifact-stream',
      acceptedHttpStatus: 202,
      archiveLimits: { mediaType: 'application/wasm', artifactBytes: wasmABI.limits.maxModuleBytes },
    });
    expect(actions['runtime.modules.list'].responseSchema.properties.items.items.properties).toHaveProperty('sha256');
    expect(adminSurfaceSchema.properties.pages.items.properties.sections.items.properties.actions.items.properties.artifactInput).toBeDefined();
    expect(adminActionsSchema.additionalProperties).toBe(false);
    expect(adminActionsSchema.required).toEqual(['runtime.modules.list', 'runtime.modules.publish']);
  });
});
