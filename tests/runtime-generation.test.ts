import { execFileSync } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');

function exerciseGenerationActivation(directory: string) {
  const output = execFileSync('go', ['run', './tests/fixtures/runtime-generation', directory], {
    cwd: root,
    env: { ...process.env, GOWORK: 'off' },
    encoding: 'utf8',
    timeout: 120_000,
  });
  return JSON.parse(output) as unknown;
}

describe('Runtime atomic configuration and WASM generation activation', () => {
  it('rejects missing or mismatched artifacts without splitting the active pair, then activates a validated pair', () => {
    const directory = mkdtempSync(join(tmpdir(), 'runtime-generation-'));
    try {
      expect(exerciseGenerationActivation(directory)).toEqual({
        missingArtifactRejected: true,
        mismatchedArtifactRejected: true,
        tamperedArtifactRejected: true,
        activePreservedBeforeCandidate: {
          generation: 'runtime-generation-1',
          groupId: 'first',
          moduleDigest: expect.any(String) as unknown,
          moduleBytesMatchDigest: true,
        },
        candidateArtifactAccepted: true,
        activePairPromotedTogether: {
          generation: 'runtime-generation-2',
          groupId: 'second',
          moduleDigest: expect.any(String) as unknown,
          moduleBytesMatchDigest: true,
        },
      });
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  }, 120_000);
});
