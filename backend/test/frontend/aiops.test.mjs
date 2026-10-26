import {test} from 'node:test';
import assert from 'node:assert/strict';
import {analyzeOps, OpsError, OpsTask, opsOutcome} from '../../../frontend/aiops.mjs';

const entry = extra => ({sequence: 1, timestamp: '2026-10-20T12:00:00+08:00', iteration: 1, agent: 'Executor', type: 'tool', status: 'completed', summary: 'not used for status', tool: 'query_prometheus_alerts', ...extra});
const json = (data, status = 200) => new Response(JSON.stringify(data), {status, headers: {'Content-Type': 'application/json'}});
const success = {message: 'OK', data: {result: '## 告警\n报告', detail: [entry({count: 0})]}};

test('AI Ops sends an empty object, preserves typed detail and checks the response contract', async () => {
  const controller = new AbortController();
  const data = await analyzeOps({signal: controller.signal, fetcher: async (url, options) => {
    assert.equal(url, '/api/ai_ops'); assert.equal(options.body, '{}'); assert.equal(options.signal, controller.signal); return json(success);
  }});
  assert.equal(data.result, success.data.result); assert.equal(data.detail[0].count, 0);
  for (const data of [{result: '', detail: []}, {result: 'report', detail: null}, {result: 'report', detail: [entry({count: '0'})]}, {result: 'report', detail: [entry({count: -1})]}]) {
    await assert.rejects(analyzeOps({fetcher: async () => json({message: 'OK', data})}), /无效/);
  }
  await assert.rejects(analyzeOps({fetcher: async () => new Response('<script>private</script>', {status: 502})}), err => !err.message.includes('script'));
});

test('errors retain partial details and no-alert status uses the latest successful typed result', async () => {
  await assert.rejects(analyzeOps({fetcher: async () => json({message: 'timed out', data: {code: 'timeout', detail: [entry({count: 0})]}}, 504)}), err => err.code === 'timeout' && err.detail.length === 1);
  assert.equal(opsOutcome([entry({count: 0})]).noAlerts, true);
  for (const events of [[], [entry({summary: 'alerts=0'})], [entry({count: 0}), entry({status: 'failed'})], [entry({count: 0}), entry({count: 1})]]) assert.equal(!!opsOutcome(events).noAlerts, false);
  assert.equal(opsOutcome([entry({count: 0}), entry({tool: 'query_prometheus_alerts', status: 'failed'})]).limited, true);
});

test('run guard, immediate cancellation and late completion isolate successive operations', async () => {
  const pending = []; let calls = 0; const states = [];
  const task = new OpsTask({request: ({signal}) => {calls++; return new Promise(resolve => pending.push({resolve, signal}));}, onChange: state => states.push(state)});
  const first = task.start(); assert.equal(await task.start(), false); assert.equal(calls, 1);
  task.cancel(); assert.equal(task.state.status, 'cancelled'); assert.equal(pending[0].signal.aborted, true);
  const second = task.start(); pending[0].resolve(success.data); await first; assert.equal(task.state.status, 'running');
  pending[1].resolve(success.data); await second; assert.equal(task.state.status, 'completed'); assert.equal(calls, 2);
  assert.equal(states.filter(s => s.status === 'completed').length, 1);
});

test('new execution clears a previous report and failures retain only this run details', async () => {
  let fail = false;
  const task = new OpsTask({request: async () => {if (fail) throw new OpsError('model failed', 'execution_failed', [entry({status: 'failed'})]); return success.data;}, onChange() {}});
  await task.start(); fail = true; const running = task.start(); assert.equal(task.state.result, ''); await running;
  assert.equal(task.state.status, 'failed'); assert.equal(task.state.result, ''); assert.equal(task.state.detail.length, 1);
  const cancelled = new OpsTask({request: async () => {throw new OpsError('request cancelled', 'cancelled');}, onChange() {}});
  await cancelled.start(); assert.equal(cancelled.state.status, 'cancelled');
});
