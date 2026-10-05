import { describe, expect, it } from 'vitest';
import { execFileSync } from 'node:child_process';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const digest = 'a'.repeat(64);

function validate(config: unknown) {
  const result = execFileSync('go', ['run', './tests/fixtures/config-probe'], {
    cwd: root,
    env: { ...process.env, GOWORK: 'off' },
    input: JSON.stringify(config),
    encoding: 'utf8',
  });
  return JSON.parse(result);
}

const valid = {
  schemaVersion: '1', groupId: 'forms', modelInstanceId: 'model',
  module: { sha256: digest },
  commands: [{ name: 'submit', export: 'invoke', tenantSites: ['tenant-a/site-1'], entities: ['entries'] }],
};

describe('Runtime configuration', () => {
  it('accepts an explicit command and artifact digest', () => {
    expect(validate(valid)).toEqual({ valid: true });
  });

  it('rejects missing tenant/site scope', () => {
    expect(validate({ ...valid, commands: [{ ...valid.commands[0], tenantSites: [] }] })).toEqual({ valid: false, error: 'missing_scope' });
  });

  it('rejects duplicate command names', () => {
    expect(validate({ ...valid, commands: [valid.commands[0], valid.commands[0]] })).toEqual({ valid: false, error: 'duplicate_command' });
  });

  it('rejects a non-digest module reference', () => {
    expect(validate({ ...valid, module: { sha256: '../../module.wasm' } })).toEqual({ valid: false, error: 'invalid_digest' });
  });
});
