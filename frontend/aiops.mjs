import {EventParser} from './sse.mjs';

export class OpsError extends Error {
  constructor(message, code = 'execution_failed', detail = []) {
    super(message); this.code = code; this.detail = detail;
  }
}

function validDetail(value) {
  return Array.isArray(value) && value.every(e => e && typeof e === 'object' &&
    Number.isSafeInteger(e.sequence) && e.sequence > 0 && Number.isSafeInteger(e.iteration) && e.iteration >= 0 &&
    ['timestamp', 'agent', 'type', 'status', 'summary'].every(k => typeof e[k] === 'string') &&
    (e.tool === undefined || typeof e.tool === 'string') &&
    (e.count === undefined || (Number.isSafeInteger(e.count) && e.count >= 0)) &&
    ['stepId', 'parentStepId', 'title', 'startedAt', 'finishedAt'].every(k => e[k] === undefined || typeof e[k] === 'string') &&
    (e.durationMs === undefined || (Number.isSafeInteger(e.durationMs) && e.durationMs >= 0)) &&
    ['input', 'output', 'error'].every(k => e[k] === undefined || (e[k] !== null && typeof e[k] === 'object' && !Array.isArray(e[k]))));
}

export async function analyzeOps({signal, fetcher = fetch} = {}) {
  const response = await fetcher('/api/ai_ops', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: '{}', signal});
  if (!response.headers.get('content-type')?.toLowerCase().includes('application/json')) {
    throw new OpsError(`服务返回了无法识别的响应（HTTP ${response.status}）。`, 'invalid_response');
  }
  let body;
  try { body = await response.json(); } catch { throw new OpsError('服务返回了无效 JSON。', 'invalid_response'); }
  const data = body?.data;
  if (!response.ok) {
    const detail = validDetail(data?.detail) ? data.detail : [];
    throw new OpsError(typeof body?.message === 'string' ? body.message : `请求失败（HTTP ${response.status}）。`,
      typeof data?.code === 'string' ? data.code : response.status === 504 ? 'timeout' : 'execution_failed', detail);
  }
  if (body?.message !== 'OK' || typeof data?.result !== 'string' || !data.result.trim() || !validDetail(data.detail)) {
    throw new OpsError('服务返回了无效的运维分析结果。', 'invalid_response');
  }
  return data;
}

export async function streamOps({signal, onConnected = () => {}, onProgress = () => {}, fetcher = fetch} = {}) {
  const response = await fetcher('/api/ai_ops_stream', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: '{}', signal});
  if (!response.ok) {
    return analyzeOps({signal, fetcher: async () => response});
  }
  if (!response.headers.get('content-type')?.toLowerCase().startsWith('text/event-stream') || !response.body) {
    await response.body?.cancel();
    throw new OpsError('服务未返回运维进度流。', 'invalid_response');
  }
  const reader = response.body.getReader(), decoder = new TextDecoder('utf-8', {fatal: true}), parser = new EventParser();
  const detail = [];
  let connected = false, result = null;
  const abort = () => { void reader.cancel().catch(() => {}); };
  signal?.addEventListener('abort', abort, {once: true});
  try {
    for (;;) {
      if (signal?.aborted) throw new DOMException('已取消分析', 'AbortError');
      const part = await reader.read();
      if (signal?.aborted) throw new DOMException('已取消分析', 'AbortError');
      for (const frame of parser.push(decoder.decode(part.value, {stream: !part.done}))) {
        if (!['connected', 'progress', 'done', 'error'].includes(frame.event)) continue;
        if (result) throw new OpsError('完成后收到额外进度消息。', 'invalid_response', detail);
        let data;
        try { data = JSON.parse(frame.data); } catch { throw new OpsError('运维进度包含无效 JSON。', 'invalid_response', detail); }
        if (!data || typeof data !== 'object' || Array.isArray(data)) throw new OpsError('运维进度格式无效。', 'invalid_response', detail);
        if (frame.event === 'connected') {
          if (connected || data.status !== 'connected') throw new OpsError('运维连接事件无效。', 'invalid_response', detail);
          connected = true; onConnected(); continue;
        }
        if (!connected) throw new OpsError('运维进度缺少连接确认。', 'invalid_response', detail);
        if (frame.event === 'progress') {
          if (!validDetail([data]) || data.sequence !== detail.length + 1 ||
              !['started', 'completed', 'failed', 'cancelled'].includes(data.status) || (!data.stepId && data.agent !== 'Runner')) {
            throw new OpsError('运维步骤或事件顺序无效。', 'invalid_response', detail);
          }
          detail.push(data); onProgress(data);
        } else if (frame.event === 'error') {
          throw new OpsError(typeof data.message === 'string' ? data.message : '分析执行失败。',
            typeof data.data?.code === 'string' ? data.data.code : 'execution_failed', validDetail(data.data?.detail) ? data.data.detail : detail);
        } else {
          if (data.status !== 'completed' || typeof data.result !== 'string' || !data.result.trim() || !validDetail(data.detail) ||
              JSON.stringify(data.detail) !== JSON.stringify(detail)) throw new OpsError('运维报告未完整确认。', 'invalid_response', detail);
          result = {result: data.result, detail: data.detail};
        }
      }
      if (result) return result;
      if (part.done) throw new OpsError('连接已结束，未收到完成确认；分析结果未确认。', 'incomplete_stream', detail);
    }
  } catch (err) {
    if (err instanceof OpsError || err.name === 'AbortError') throw err;
    throw new OpsError('连接已中断，分析结果未确认。请检查网络后手动重试。', 'network_error', detail);
  } finally {
    signal?.removeEventListener('abort', abort);
    await reader.cancel().catch(() => {}); reader.releaseLock();
  }
}

export function opsSteps(detail, status, clocks = new Map(), now = performance.now()) {
  const steps = new Map();
  for (const event of detail) {
    if (event.agent === 'Runner') continue;
    const id = event.stepId || `event-${event.sequence}`;
    const before = steps.get(id);
    steps.set(id, {...before, ...event, stepId: id, receivedAt: clocks.get(id) ?? before?.receivedAt ?? now});
  }
  return [...steps.values()].map(step => {
    const live = step.status === 'started';
    return {...step, status: live && status !== 'running' ? status === 'cancelled' ? 'cancelled' : 'interrupted' : step.status,
      elapsedMs: step.durationMs ?? Math.max(0, now - step.receivedAt)};
  });
}

export function opsOutcome(detail) {
  const alert = detail.filter(e => e.type === 'tool' && e.tool === 'query_prometheus_alerts').at(-1);
  const alertsKnown = Boolean(alert?.status === 'completed' && Number.isSafeInteger(alert.count));
  return {noAlerts: alertsKnown && alert.count === 0, alertsKnown,
    limited: !alertsKnown || detail.some(e => e.type === 'tool' && e.status === 'failed')};
}

export class OpsTask {
  constructor({request = streamOps, onChange, now = () => performance.now()}) {
    this.request = request; this.onChange = onChange; this.now = now; this.active = null;
    this.runId = 0; this.clocks = new Map();
    this.state = {runId: 0, status: 'idle', result: '', detail: [], steps: [], startedAt: null, finishedAt: null};
  }
  update(value) {
    this.state = {...this.state, ...value};
    this.state.steps = opsSteps(this.state.detail, this.state.status, this.clocks, this.state.finishedAt ?? this.now());
    this.onChange(this.state);
  }
  async start() {
    if (this.active) return false;
    const op = {controller: new AbortController()}; this.active = op;
    this.clocks.clear();
    this.update({runId: ++this.runId, status: 'running', result: '', detail: [], code: '', message: '', startedAt: this.now(), finishedAt: null});
    try {
      const data = await this.request({signal: op.controller.signal, onProgress: event => {
        if (this.active !== op) return;
        if (!this.clocks.has(event.stepId)) this.clocks.set(event.stepId, this.now());
        this.update({detail: [...this.state.detail, event]});
      }});
      // Cancellation unlocks the page immediately; late responses cannot alter a newer run.
      if (this.active !== op) return false;
      this.active = null;
      this.update({status: 'completed', result: data.result, detail: data.detail, finishedAt: this.now()});
    } catch (err) {
      if (this.active !== op) return false;
      this.active = null;
      this.update({status: op.controller.signal.aborted || err.code === 'cancelled' ? 'cancelled' : 'failed', code: err.code || 'network_error',
        message: err instanceof TypeError ? '网络连接失败，分析结果未确认。' : err.message,
        detail: err.detail?.length ? err.detail : this.state.detail, finishedAt: this.now()});
    }
    return true;
  }
  cancel() {
    if (!this.active) return;
    const op = this.active; this.active = null; op.controller.abort();
    this.update({status: 'cancelled', code: 'cancelled', result: '', message: '已取消分析，未生成有效报告。不会自动重试。', finishedAt: this.now()});
  }
}
