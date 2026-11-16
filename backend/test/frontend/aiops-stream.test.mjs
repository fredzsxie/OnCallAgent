import {test} from 'node:test';
import assert from 'node:assert/strict';
import {streamOps, OpsTask, opsSteps} from '../../../frontend/aiops.mjs';
import {resultFields, durationText} from '../../../frontend/aiops-view.mjs';

const event = extra => ({sequence: 1, timestamp: '2026-11-16T08:00:00Z', iteration: 1, agent: 'Executor', type: 'execute', status: 'started', summary: '查询告警', stepId: 'step-1', title: '查询告警', ...extra});
const frame = (name, data) => `event: ${name}\ndata: ${JSON.stringify(data)}\n\n`;
const connected = frame('connected', {status: 'connected'});
const done = detail => frame('done', {status: 'completed', result: '## 告警\n中文报告', detail});
const encoder = new TextEncoder();
function response(content, size = 1) {
  const bytes = encoder.encode(content);
  return new Response(new ReadableStream({start(c) {for (let i = 0; i < bytes.length; i += size) c.enqueue(bytes.slice(i, i + size)); c.close();}}), {headers: {'Content-Type': 'text/event-stream'}});
}

test('Ops SSE handles UTF-8 boundaries, comments and preserves typed final evidence', async () => {
  const detail = [event(), event({sequence: 2, status: 'completed', durationMs: 1523, finishedAt: '2026-11-16T08:00:01.523Z'})];
  for (const size of [1, 2, 7, 65536]) {
    const seen = [];
    const out = await streamOps({onProgress: e => seen.push(e), fetcher: async (url, options) => {
      assert.equal(url, '/api/ai_ops_stream'); assert.equal(options.method, 'POST'); assert.equal(options.body, '{}');
      return response(': heartbeat\r\n\r\n' + connected + detail.map(e => frame('progress', e)).join('') + done(detail), size);
    }});
    assert.deepEqual(seen, detail); assert.deepEqual(out.detail, detail); assert.match(out.result, /中文报告/);
  }
});

test('progress updates task before completion; start/end merge and elapsed freezes on failure', async () => {
  let publish, finish, clock = 100;
  const task = new OpsTask({now: () => clock, onChange() {}, request: ({onProgress}) => {publish = onProgress; return new Promise(resolve => {finish = resolve;});}});
  const run = task.start(); publish(event()); clock = 600;
  assert.equal(task.state.status, 'running'); assert.equal(task.state.result, ''); assert.equal(task.state.steps.length, 1);
  publish(event({sequence: 2, status: 'completed', durationMs: 900}));
  assert.equal(task.state.steps.length, 1); assert.equal(task.state.steps[0].elapsedMs, 900);
  finish({result: 'report', detail: task.state.detail}); await run; assert.equal(task.state.status, 'completed');
  const sameTools = [event(), event({sequence: 2, stepId: 'tool-1', parentStepId: 'step-1', type: 'tool'}), event({sequence: 3, stepId: 'tool-2', parentStepId: 'step-1', type: 'tool'})];
  const steps = opsSteps(sameTools, 'failed', new Map([['step-1', 100], ['tool-1', 300], ['tool-2', 400]]), 600);
  assert.equal(steps.length, 3); assert.equal(steps[1].elapsedMs, 300); assert.equal(steps[2].status, 'interrupted');
});

test('Ops SSE rejects malformed, reordered and unconfirmed streams while retaining progress', async () => {
  const e = event();
  const cases = [
    ['', /确认/], [connected + frame('progress', e), /确认/],
    [frame('progress', e) + done([e]), /连接/],
    [connected + frame('progress', {...e, sequence: 2}), /顺序/],
    [connected + frame('progress', {...e, durationMs: -1}), /步骤/],
    [connected + 'event: progress\ndata: invalid\n\n', /JSON/],
    [connected + frame('progress', e) + done([]), /完整/],
    [connected + frame('progress', e) + done([e]) + frame('progress', e), /额外/],
  ];
  for (const [source, error] of cases) await assert.rejects(streamOps({fetcher: async () => response(source, 65536)}), error);
  await assert.rejects(streamOps({fetcher: async () => response(connected + frame('progress', e) + frame('error', {message: 'timed out', data: {code: 'timeout', detail: [e]}}))}), err => err.code === 'timeout' && err.detail.length === 1);
  await assert.rejects(streamOps({fetcher: async () => new Response(JSON.stringify({message: 'invalid', data: {code: 'invalid_input', detail: []}}), {status: 400, headers: {'Content-Type': 'application/json'}})}), err => err.code === 'invalid_input');
  await assert.rejects(streamOps({fetcher: async () => new Response('unexpected')}), /进度流/);
});

test('cancel releases stream resources and late progress cannot affect a new task', async () => {
  let cancelled = 0;
  const controller = new AbortController();
  const body = new ReadableStream({start(c) {c.enqueue(encoder.encode(connected));}, cancel() {cancelled++;}});
  await assert.rejects(streamOps({signal: controller.signal, onConnected: () => controller.abort(), fetcher: async () => new Response(body, {headers: {'Content-Type': 'text/event-stream'}})}), {name: 'AbortError'});
  assert.equal(cancelled, 1); assert.equal(body.locked, false);
  const pending = [];
  const task = new OpsTask({onChange() {}, request: ({onProgress}) => new Promise(resolve => pending.push({resolve, onProgress}))});
  const first = task.start(); pending[0].onProgress(event()); task.cancel();
  assert.equal(task.state.steps[0].status, 'cancelled');
  const second = task.start(); pending[0].onProgress(event({sequence: 2})); pending[0].resolve({result: 'old', detail: []}); await first;
  assert.equal(task.state.detail.length, 0); assert.equal(task.state.status, 'running');
  pending[1].onProgress(event()); pending[1].resolve({result: 'new', detail: task.state.detail}); await second; assert.equal(task.state.result, 'new');
});

test('partial progress survives read failure and is never shown as successful', async () => {
  let c;
  const body = new ReadableStream({start(value) {c = value;}});
  const task = new OpsTask({onChange() {}, request: options => streamOps({...options, fetcher: async () => new Response(body, {headers: {'Content-Type': 'text/event-stream'}})})});
  const run = task.start(); c.enqueue(encoder.encode(connected + frame('progress', event())));
  await new Promise(resolve => setTimeout(resolve, 0)); assert.equal(task.state.detail.length, 1);
  c.error(new TypeError('network read failed')); await run;
  assert.equal(task.state.status, 'failed'); assert.equal(task.state.result, ''); assert.equal(task.state.detail.length, 1); assert.equal(task.state.steps[0].status, 'interrupted');
});

test('structured result presentation includes actual evidence and truncation', () => {
  const fields = resultFields({input: {query: 'HighCPU'}, count: 25, output: {source: 'prom', timestamp: '2026-11-16T08:00:00Z', timezone: 'UTC', unix_seconds: 0,
    alerts: [{labels: {alertname: 'HighCPU', instance: 'node-a'}, state: 'firing', annotations: {summary: '<script>text</script>'}}],
    documents: [{title: 'SOP', source: 'sop.md', sectionPath: '诊断', version: 'v2', score: .9, scoreType: 'cosine', excerpt: '命中内容', truncated: true}], truncated: true}});
  const values = fields.map(e => e[1]).join('\n');
  for (const expected of ['HighCPU', 'node-a', 'sop.md', '命中内容', '25', '20', '已截断', '<script>text</script>']) assert.ok(values.includes(expected));
  assert.equal(durationText(1500), '1.5 秒'); assert.equal(durationText(61200), '1 分 1 秒');
});
