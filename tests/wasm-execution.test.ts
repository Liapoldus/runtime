import { describe, expect, it } from 'vitest';
import { execFileSync } from 'node:child_process';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
// Minimal WASM module exporting memory, alloc and invoke. invoke returns the
// input pointer and length in the v1 ABI; it is intentionally host-function-free.
const echoWasm = Buffer.from(
  '0061736d01000000' +
  '010c0260017f017f60027f7f017e' +
  '0303020001' +
  '0503010001' +
  '071b03066d656d6f7279020005616c6c6f63000006696e766f6b650001' +
  '0a1302040041000b0c002001ad4220862000ad840b',
  'hex',
);

function execute(wasm: Buffer, input: unknown) {
  const output = execFileSync('go', ['run', './tests/fixtures/wasm-probe'], {
    cwd: root, env: { ...process.env, GOWORK: 'off' },
    input: JSON.stringify({ wasm: wasm.toString('base64'), input }), encoding: 'utf8',
  });
  return JSON.parse(output);
}

describe('Runtime JSON WASM ABI', () => {
  it('executes an isolated module with bounded JSON input/output', () => {
    expect(execute(echoWasm, { message: 'hello' })).toEqual({ output: { message: 'hello' } });
  });

  it('rejects a module without the required ABI exports', () => {
    expect(execute(Buffer.from('0061736d01000000', 'hex'), { message: 'hello' })).toEqual({ error: 'invalid_abi' });
  });
});
