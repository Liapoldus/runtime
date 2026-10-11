import requestSchema from '../contracts/v2/invoke-request.schema.json';
import successSchema from '../contracts/v2/invoke-success.schema.json';
import errors from '../contracts/v2/errors-invoke.json';
import { describe, expect, it } from 'vitest';




const identifierPattern = '^[A-Za-z][A-Za-z0-9_]{0,63}$';

describe('runtime.invoke contract', () => {
  it('defines the request document the host validates before execution', () => {
    expect(requestSchema.type).toBe('object');
    expect(requestSchema.additionalProperties).toBe(false);
    expect(requestSchema.required).toEqual(['command', 'input']);
    expect(requestSchema.properties.command.$ref).toBe('#/$defs/identifier');
    expect(requestSchema.$defs.identifier).toMatchObject({ type: 'string', pattern: identifierPattern });
    expect(requestSchema.properties.entities.minItems).toBe(0);
    expect(requestSchema.properties.entities.maxItems).toBe(128);
    expect(requestSchema.properties.entities.uniqueItems).toBe(true);
    expect(requestSchema.properties.entities.items.$ref).toBe('#/$defs/identifier');
    expect(requestSchema.properties.input).toEqual({});
  });

  it('defines the success document the host wraps the WASM output in', () => {
    expect(successSchema.additionalProperties).toBe(false);
    expect(successSchema.required).toEqual(['command', 'output']);
    expect(successSchema.properties.command.pattern).toBe(identifierPattern);
    expect(successSchema.properties.output).toEqual({});
  });

  it('pins every error code to a single http status and a replay hint', () => {
    expect(errors.version).toBe(2);
    expect(errors.capability).toBe('runtime.invoke');
    expect(errors.semantics).toContain('replay');
    const codes = Object.entries(errors.errors) as [string, { http: number; retryable: boolean }][];
    expect(new Set(codes.map(([code]) => code))).toEqual(new Set([
      'not_ready', 'unknown_command', 'forbidden_entity',
      'invalid_request', 'payload_too_large', 'execution_failed', 'execution_timeout', 'cancelled', 'overloaded',
    ]));
    expect(codes).toEqual(expect.arrayContaining([
      ['not_ready', { http: 503, retryable: true }],
      ['unknown_command', { http: 404, retryable: false }],
      ['forbidden_entity', { http: 403, retryable: false }],
      ['invalid_request', { http: 422, retryable: false }],
      ['payload_too_large', { http: 413, retryable: false }],
      ['execution_failed', { http: 502, retryable: false }],
      ['execution_timeout', { http: 504, retryable: false }],
      ['cancelled', { http: 499, retryable: false }],
      ['overloaded', { http: 503, retryable: true }],
    ]));
    for (const [code, { http, retryable }] of codes) {
      expect(code.length).toBeGreaterThan(0);
      expect(http).toBeGreaterThanOrEqual(400);
      expect(http).toBeLessThanOrEqual(599);
      expect(typeof retryable).toBe('boolean');
    }
  });
});
