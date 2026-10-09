import { describe, expect, it } from 'vitest';
import { execFileSync } from 'node:child_process';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');

type ReloadResult = { applied: boolean; outcome: string };
type ReadinessResult = { generation: string; pendingGeneration: string; ready: boolean };

interface ScenarioResult {
  phase1: {
    uploadAccepted: boolean;
    catalog: { status: number; count: number };
    reload: ReloadResult;
    readiness: ReadinessResult;
  };
  phase2: {
    pullWhileCoreDown: ReloadResult;
    readinessWhileCoreDown: ReadinessResult;
    catalogAfterRestart: { status: number; count: number };
    reloadResumed: ReloadResult;
    readinessResumed: ReadinessResult;
    repeatAnnounce: ReloadResult;
    readinessAfterRepeat: ReadinessResult;
    reloadNext: ReloadResult;
    readinessNext: ReadinessResult;
  };
}

function exerciseRuntimeRestartReconnect(): ScenarioResult {
  const output = execFileSync('go', ['run', './tests/fixtures/runtime-restart-reconnect'], {
    cwd: root,
    env: { ...process.env, GOWORK: 'off' },
    encoding: 'utf8',
    timeout: 120_000,
  });
  return JSON.parse(output) as ScenarioResult;
}

describe('Runtime restart & Core reconnect integration', () => {
  it('persists artifacts, refuses exact-generation reload while Core is down, resumes after reconnect, and stays idempotent on repeat announce', () => {
    const result = exerciseRuntimeRestartReconnect();

    expect(result.phase1.uploadAccepted).toBe(true);
    expect(result.phase1.catalog).toEqual({ status: 200, count: 1 });
    expect(result.phase1.reload).toEqual({ applied: true, outcome: 'applied' });
    expect(result.phase1.readiness).toEqual({
      generation: 'runtime-restart-1',
      pendingGeneration: '',
      ready: true,
    });

    // Replica restarted into a Core that is offline: the exact-generation pull is
    // refused cleanly, the replica stays alive, and readiness reports the pending pair.
    expect(result.phase2.pullWhileCoreDown.applied).toBe(false);
    expect(result.phase2.readinessWhileCoreDown).toEqual({
      generation: '',
      pendingGeneration: 'runtime-restart-1',
      ready: false,
    });

    // The module file survived the replica restart on disk, so the catalog is restored.
    expect(result.phase2.catalogAfterRestart).toEqual({ status: 200, count: 1 });

    // Core returns: the resumed replica re-pulls the exact generation without re-upload.
    expect(result.phase2.reloadResumed).toEqual({ applied: true, outcome: 'applied' });
    expect(result.phase2.readinessResumed).toEqual({
      generation: 'runtime-restart-1',
      pendingGeneration: '',
      ready: true,
    });

    // Re-announcing the exact active generation is idempotent, not a churn.
    expect(result.phase2.repeatAnnounce).toEqual({ applied: true, outcome: 'alreadyActive' });
    expect(result.phase2.readinessAfterRepeat).toEqual({
      generation: 'runtime-restart-1',
      pendingGeneration: '',
      ready: true,
    });

    // The reconnected replica accepts the following generation.
    expect(result.phase2.reloadNext).toEqual({ applied: true, outcome: 'applied' });
    expect(result.phase2.readinessNext).toEqual({
      generation: 'runtime-restart-2',
      pendingGeneration: '',
      ready: true,
    });
  }, 120_000);
});
