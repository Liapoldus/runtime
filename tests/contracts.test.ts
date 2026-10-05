import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const manifest = JSON.parse(readFileSync(resolve(root, 'contracts/v1/plugin.json'), 'utf8'));

describe('product contract ownership', () => {
  it('declares peer invoke and a terminal HTTP adapter in this plugin', () => {
    expect(manifest.capabilities).toEqual([
      { name: 'runtime.invoke', mode: 'call' },
      { name: 'runtime.http', mode: 'call' },
    ]);
  });
});
