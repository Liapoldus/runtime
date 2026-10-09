import { describe, expect, it } from 'vitest';
import { execFileSync } from 'node:child_process';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');

interface Outcome {
  status?: number;
  body?: unknown;
  code?: string;
  transport?: string;
  err?: string;
  command?: string;
  output?: unknown;
}

interface ScenarioResult {
  notReady: Outcome;
  success: Outcome;
  successWithEntity: Outcome;
  unknownCommand: Outcome;
  forbiddenScope: Outcome;
  forbiddenEntity: Outcome;
  invalidRequest: Outcome;
  payloadTooLarge: Outcome;
  unknownMethod: Outcome;
  unauthorized: Outcome;
  cancelled: Outcome;
  timeout: Outcome;
  envelope: Outcome;
  shutdownDrain: {
    acceptedCall: Outcome;
    refusedAfterDrain: Outcome;
    processExitedAfterCall: boolean;
  };
}

function runFixture(): ScenarioResult {
  const stdout = execFileSync('go', ['run', './tests/fixtures/peer-invoke'], {
    cwd: root,
    env: { ...process.env, GOWORK: 'off' },
    encoding: 'utf8',
    timeout: 180_000,
    maxBuffer: 4 << 20,
  });
  return JSON.parse(stdout) as ScenarioResult;
}

describe('runtime.invoke peer surface', () => {
  const results = runFixture();

  it('refuses invocation before the first applied generation', () => {
    expect(results.notReady).toMatchObject({ status: 503, code: 'not_ready' });
  });

  it('executes a command and returns the composed host input as output', () => {
    expect(results.success).toMatchObject({ status: 200, command: 'echo' });
    const output = results.success.output as Record<string, unknown>;
    expect(output).toMatchObject({
      command: 'echo',
      scope: 'tenant-a/site-1',
      entities: [],
      input: { message: 'hello' },
    });
  });

  it('passes declared entities through to the module', () => {
    const output = results.successWithEntity.output as Record<string, unknown>;
    expect(output.entities).toEqual(['entity1']);
    expect(output.input).toMatchObject({ message: 'go' });
  });

  it('rejects an undeclared command', () => {
    expect(results.unknownCommand).toMatchObject({ status: 404, code: 'unknown_command' });
  });

  it('rejects a scope outside the command allowlist', () => {
    expect(results.forbiddenScope).toMatchObject({ status: 403, code: 'forbidden_scope' });
  });

  it('rejects an entity outside the command allowlist', () => {
    expect(results.forbiddenEntity).toMatchObject({ status: 403, code: 'forbidden_entity' });
  });

  it('rejects a malformed request document', () => {
    expect(results.invalidRequest).toMatchObject({ status: 422, code: 'invalid_request' });
  });

  it('bounds the composed input sent to the module', () => {
    expect(results.payloadTooLarge).toMatchObject({ status: 413, code: 'payload_too_large' });
  });

  it('refuses a method the runtime does not serve', () => {
    expect(results.unknownMethod.transport).toBe('method_not_found');
  });

  it('refuses a caller outside the pinned identity', () => {
    expect(results.unauthorized.transport).toBe('unauthorized');
  });

  it('propagates cancellation of an in-flight execution', () => {
    expect(results.cancelled.status).not.toBe(200);
    expect(results.cancelled.code === 'cancelled' || results.cancelled.transport === 'cancelled').toBe(true);
  });

  it('enforces the bounded execution deadline', () => {
    expect(results.timeout).toMatchObject({ status: 504, code: 'execution_timeout' });
  });

  it('accepts a Server v1 request envelope', () => {
    expect(results.envelope).toMatchObject({ status: 200, command: 'echo' });
    const output = results.envelope.output as Record<string, unknown>;
    expect(output.input).toMatchObject({ message: 'wrapped' });
  });

  it('drains an admitted peer invocation on shutdown and fences new invocations', () => {
    expect(results.shutdownDrain).toMatchObject({
      acceptedCall: { status: 504, code: 'execution_timeout' },
      refusedAfterDrain: { status: 503, code: 'not_ready' },
      processExitedAfterCall: true,
    });
  });
});
