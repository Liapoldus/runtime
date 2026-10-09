import { describe, expect, it } from 'vitest';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createHash } from 'node:crypto';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const moduleBytes = Buffer.from(
  '0061736d01000000' +
  '010c0260017f017f60027f7f017e' +
  '0303020001' +
  '0503010001' +
  '071b03066d656d6f7279020005616c6c6f63000006696e766f6b650001' +
  '0a1302040041000b0c002001ad4220862000ad840b',
  'hex',
);

function invoke(digest: string) {
  const artifactDirectory = mkdtempSync(join(tmpdir(), 'runtime-module-action-'));
  try {
    const output = execFileSync('go', ['run', './tests/fixtures/module-artifact-action', artifactDirectory, digest], {
      cwd: root,
      env: { ...process.env, GOWORK: 'off' },
      input: moduleBytes,
      encoding: 'utf8',
      timeout: 120_000,
    });
    return JSON.parse(output) as unknown;
  } finally {
    rmSync(artifactDirectory, { recursive: true, force: true });
  }
}

describe('Runtime module Admin Surface', () => {
  it('publishes a validated content-addressed module through ArtifactAcceptor and lists it', () => {
    const digest = createHash('sha256').update(moduleBytes).digest('hex');
    expect(invoke(digest)).toEqual({
      accepted: { status: 202, body: { sha256: digest } },
      listed: { status: 200, body: { items: [{ sha256: digest, sizeBytes: moduleBytes.length }], nextCursor: null } },
    });
  });

  it('rejects an incorrect digest without publishing a module', () => {
    expect(invoke('a'.repeat(64))).toMatchObject({
      accepted: { status: 409, body: { code: 'digest_mismatch' } },
      listed: { status: 200, body: { items: [], nextCursor: null } },
    });
  });
});
