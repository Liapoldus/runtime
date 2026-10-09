import { describe, expect, it } from 'vitest';
import { execFileSync } from 'node:child_process';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');

const MAX_INPUT_BYTES = 1048576;
const JSON_OVERHEAD = '{"data":""}'.length; // 11

interface GateResult {
  ok: boolean;
  error?: string;
  output?: unknown;
  panic?: string;
}

interface StepResult {
  name: string;
  validate?: GateResult;
  execute?: GateResult;
}

function runSteps(steps: Array<{ module: string; validate?: boolean; execute?: boolean; input?: unknown }>): StepResult[] {
  const output = execFileSync('go', ['run', './tests/fixtures/sandbox-adversarial'], {
    cwd: root, env: { ...process.env, GOWORK: 'off' },
    input: JSON.stringify({ steps }), encoding: 'utf8', maxBuffer: 16 * 1024 * 1024,
  });
  return (JSON.parse(output) as { results: StepResult[] }).results;
}

function single(module: string, opts: { validate?: boolean; execute?: boolean; input?: unknown } = {}): StepResult {
  return runSteps([{ module, validate: true, execute: true, input: {} , ...opts }])[0];
}

describe('Runtime adversarial sandbox conformance', () => {
  it('rejects host-function imports (env) without instantiating or executing', () => {
    const result = single('import-env');
    expect(result.validate).toEqual({ ok: false, error: 'invalid_abi' });
    expect(result.execute).toEqual({ ok: false, error: 'invalid_abi' });
  }, 30000);

  it('rejects WASI imports (wasi_snapshot_preview1) the same way', () => {
    const result = single('import-wasi');
    expect(result.validate).toEqual({ ok: false, error: 'invalid_abi' });
    expect(result.execute).toEqual({ ok: false, error: 'invalid_abi' });
  });

  it('rejects non-function host imports before admitting the module', () => {
    const result = single('import-global');
    expect(result.validate).toEqual({ ok: false, error: 'invalid_abi' });
    expect(result.execute).toEqual({ ok: false, error: 'invalid_abi' });
  });

  it('rejects a result length beyond the output bound even when the pointer is in range', () => {
    const result = single('bogus-length');
    expect(result.validate).toEqual({ ok: true });
    expect(result.execute).toEqual({ ok: false, error: 'payload_too_large' });
  });

  it('rejects a result pointer far outside memory as an invalid ABI contract', () => {
    const result = single('bogus-pointer');
    expect(result.validate).toEqual({ ok: true });
    expect(result.execute).toEqual({ ok: false, error: 'invalid_abi' });
  });

  it('rejects non-JSON output bytes read from module memory', () => {
    const result = single('non-json');
    expect(result.validate).toEqual({ ok: true });
    expect(result.execute).toEqual({ ok: false, error: 'invalid_json' });
  });

  it('interrupts a host-function-free infinite loop in invoke', () => {
    const result = single('invoke-loop');
    expect(result.validate).toEqual({ ok: true });
    expect(result.execute).toEqual({ ok: false, error: 'execution_timeout' });
  }, 15000);

  it('fails allocation returning an out-of-range pointer instead of corrupting memory', () => {
    const result = single('alloc-range');
    expect(result.validate).toEqual({ ok: true });
    expect(result.execute).toEqual({ ok: false, error: 'invalid_abi' });
  });

  it('turns unbounded recursion in invoke into a failed execution trap', () => {
    const result = single('recursion');
    expect(result.validate).toEqual({ ok: true });
    expect(result.execute).toEqual({ ok: false, error: 'execution_failed' });
  });

  it('stays panic-free across every adversarial module and gate', () => {
    const results = runSteps([
      { module: 'import-env', validate: true, execute: true, input: {} },
      { module: 'import-wasi', validate: true, execute: true, input: {} },
      { module: 'import-global', validate: true, execute: true, input: {} },
      { module: 'bogus-length', validate: true, execute: true, input: {} },
      { module: 'bogus-pointer', validate: true, execute: true, input: {} },
      { module: 'non-json', validate: true, execute: true, input: {} },
      { module: 'invoke-loop', validate: true, execute: true, input: {} },
      { module: 'alloc-range', validate: true, execute: true, input: {} },
      { module: 'recursion', validate: true, execute: true, input: {} },
    ]);
    for (const result of results) {
      expect(result.validate?.panic ?? result.execute?.panic, `${result.name} panicked`).toBeUndefined();
    }
  }, 20000);

  it('accepts a control module that echoes and yields its input back', () => {
    const result = single('echo', { input: { message: 'hello' } });
    expect(result.validate).toEqual({ ok: true });
    expect(result.execute).toEqual({ ok: true, output: { message: 'hello' } });
  });

  it('accepts an input at exactly the boundary and rejects one byte beyond it', () => {
    const atLimit = single('echo-16', { input: { data: 'x'.repeat(MAX_INPUT_BYTES - JSON_OVERHEAD) } });
    expect(atLimit.execute).toEqual({ ok: true, output: { data: 'x'.repeat(MAX_INPUT_BYTES - JSON_OVERHEAD) } });
    const overLimit = single('echo-16', { input: { data: 'x'.repeat(MAX_INPUT_BYTES - JSON_OVERHEAD + 1) } });
    expect(overLimit.execute).toEqual({ ok: false, error: 'payload_too_large' });
  }, 20000);
});
