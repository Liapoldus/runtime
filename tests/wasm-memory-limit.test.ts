import abi from '../contracts/v1/wasm-abi.json';
import errors from '../contracts/v1/wasm-errors.json';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFileSync } from 'node:child_process';
import { describe, expect, it } from 'vitest';


const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');

describe('WASM hard memory boundary', () => {
  it('declares the existing bounded execution error in the v1 owner contract', () => {



    expect(abi.limits.maxMemoryPages).toBe(1024);
    expect(errors.version).toBe(1);
    expect(errors.errors.execution_failed.code).toBe('execution_failed');
    expect(errors.errors.execution_failed.maximumMessageBytes).toBe(0);
  });

  it('bounds memory.grow beyond the contract cap and preserves the active configuration', () => {
    const result: unknown = JSON.parse(execFileSync('go', ['run', './tests/fixtures/wasm-memory-limit'], {
      cwd: root,
      env: { ...process.env, GOWORK: 'off' },
      encoding: 'utf8',
      timeout: 120_000,
    }));

    expect(result).toEqual({
      error: 'execution_failed',
      attemptedGrowthPages: 1024,
      configuredMaximumPages: 1024,
      activeConfigurationPreserved: true,
      activeGeneration: 'memory-limit-baseline',
    });
  }, 120_000);
});
