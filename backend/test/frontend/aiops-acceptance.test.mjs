import {test} from 'node:test';
import assert from 'node:assert/strict';
import {streamOps, opsOutcome} from '../../../frontend/aiops.mjs';

// Opt in only against TestFrontendServerHelper and the local Postman simulation.
const base = process.env.ONCALL_AIOPS_ACCEPTANCE_URL;
const helper = process.env.ONCALL_AIOPS_HELPER_URL;
test('local real Agent and proxy deliver live progress, typed evidence and safe failures', {
  skip: !base || !helper, timeout: 30000,
}, async t => {
  for (const value of [base, helper]) assert.ok(['127.0.0.1', 'localhost', '[::1]'].includes(new URL(value).hostname));
  const fetcher = (path, options) => fetch(new URL(path, base), options);
  const select = async scenario => {
    const response = await fetch(new URL('/__test/ops-scenario', helper), {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({scenario})});
    assert.equal(response.status, 200);
  };
  try {
    for (const scenario of ['normal', 'empty', 'limited', 'failure', 'timeout', 'slow']) {
      await t.test(scenario, async () => {
        await select(scenario);
        const seen = [], started = performance.now(); let first;
        const run = () => streamOps({fetcher, onProgress(event) {first ??= performance.now(); seen.push(event);}});
        if (['failure', 'timeout'].includes(scenario)) {
          await assert.rejects(run(), err => err.code === (scenario === 'failure' ? 'execution_failed' : 'timeout') && err.detail.length > 0);
          return;
        }
        const out = await run(); assert.deepEqual(seen, out.detail);
        const pairs = new Map();
        for (const event of seen) {
          if (!event.stepId) continue;
          if (event.status === 'started') {assert.ok(!pairs.has(event.stepId)); pairs.set(event.stepId, 1);}
          else {assert.equal(pairs.get(event.stepId), 1); pairs.set(event.stepId, 2); assert.ok(event.durationMs >= 0);}
        }
        assert.ok([...pairs.values()].every(n => n === 2));
        const outcome = opsOutcome(out.detail);
        assert.equal(outcome.noAlerts, scenario === 'empty'); assert.equal(outcome.limited, scenario === 'limited');
        if (scenario === 'normal') {
          const alerts = seen.find(e => e.tool === 'query_prometheus_alerts' && e.status === 'completed');
          assert.equal(alerts.count, 2); assert.equal(alerts.output.alerts.length, 2);
          assert.notEqual(alerts.output.alerts[0].labels.instance, alerts.output.alerts[1].labels.instance);
        }
        if (scenario === 'slow') {assert.ok(first - started < 2000); assert.ok(performance.now() - first > 5000);}
        t.diagnostic(`${scenario}: ${seen.length} events, ${pairs.size} paired steps, completed report`);
      });
    }
  } finally { await select('normal'); }
});
