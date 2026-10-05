import { describe, expect, it } from 'vitest';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createHash } from 'node:crypto';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const minimalWasm = Buffer.from('0061736d01000000', 'hex');

function upload(data: Buffer, expected: string) {
  const directory = mkdtempSync(join(tmpdir(), 'runtime-artifact-'));
  try {
    const output = execFileSync('go', ['run', './tests/fixtures/artifact-probe', directory, expected], {
      cwd: root, env: { ...process.env, GOWORK: 'off' }, input: data, encoding: 'utf8',
    });
    return JSON.parse(output);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}

describe('immutable WASM artifact admission', () => {
  it('stores only a valid digest-matched WASM module', () => {
    const digest = createHash('sha256').update(minimalWasm).digest('hex');
    expect(upload(minimalWasm, digest)).toEqual({ accepted: true, digest });
  });

  it('rejects a digest mismatch', () => {
    expect(upload(minimalWasm, 'a'.repeat(64))).toEqual({ accepted: false, error: 'digest_mismatch' });
  });

  it('rejects non-WASM bytes', () => {
    const data = Buffer.from('{"not":"wasm"}');
    expect(upload(data, createHash('sha256').update(data).digest('hex'))).toEqual({ accepted: false, error: 'invalid_module' });
  });
});
