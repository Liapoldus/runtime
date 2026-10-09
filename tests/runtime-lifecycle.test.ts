import { describe, expect, it } from 'vitest';
import { execFileSync } from 'node:child_process';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');

function exerciseReloadLifecycle() {
  const output = execFileSync('go', ['run', './tests/fixtures/runtime-lifecycle'], {
    cwd: root,
    env: { ...process.env, GOWORK: 'off' },
    encoding: 'utf8',
    timeout: 120_000,
  });
  return JSON.parse(output) as unknown;
}

describe('Runtime Plugin SDK Reload integration', () => {
  it('rejects an invalid candidate without replacing active settings, then atomically activates a valid generation', () => {
    expect(exerciseReloadLifecycle()).toEqual({
      initial: { generation: 'runtime-generation-1', pendingGeneration: '', ready: true },
      invalid: { applied: false, outcome: 'applyRejected' },
      afterInvalid: { generation: 'runtime-generation-1', pendingGeneration: 'runtime-generation-2', ready: false },
      valid: { applied: true, outcome: 'applied' },
      afterValid: { generation: 'runtime-generation-3', pendingGeneration: '', ready: true },
      artifacts: { accepted: 2, status: 202 },
      moduleCatalog: { status: 200, count: 2 },
    });
  }, 120_000);
});
