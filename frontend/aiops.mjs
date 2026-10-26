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
    (e.count === undefined || (Number.isSafeInteger(e.count) && e.count >= 0)));
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

export function opsOutcome(detail) {
  const alert = detail.filter(e => e.type === 'tool' && e.tool === 'query_prometheus_alerts').at(-1);
  const alertsKnown = Boolean(alert?.status === 'completed' && Number.isSafeInteger(alert.count));
  return {noAlerts: alertsKnown && alert.count === 0, alertsKnown,
    limited: !alertsKnown || detail.some(e => e.type === 'tool' && e.status === 'failed')};
}

export class OpsTask {
  constructor({request = analyzeOps, onChange, now = Date.now}) {
    this.request = request; this.onChange = onChange; this.now = now; this.active = null;
    this.state = {status: 'idle', result: '', detail: [], startedAt: null, finishedAt: null};
  }
  update(value) { this.state = {...this.state, ...value}; this.onChange(this.state); }
  async start() {
    if (this.active) return false;
    const op = {controller: new AbortController()}; this.active = op;
    this.update({status: 'running', result: '', detail: [], code: '', message: '', startedAt: this.now(), finishedAt: null});
    try {
      const data = await this.request({signal: op.controller.signal});
      // Cancellation unlocks the page immediately; late responses cannot alter a newer run.
      if (this.active !== op) return false;
      this.active = null;
      this.update({status: 'completed', result: data.result, detail: data.detail, finishedAt: this.now()});
    } catch (err) {
      if (this.active !== op) return false;
      this.active = null;
      this.update({status: op.controller.signal.aborted || err.code === 'cancelled' ? 'cancelled' : 'failed', code: err.code || 'network_error',
        message: err instanceof TypeError ? '网络连接失败，分析结果未确认。' : err.message,
        detail: err.detail || [], finishedAt: this.now()});
    }
    return true;
  }
  cancel() {
    if (!this.active) return;
    const op = this.active; this.active = null; op.controller.abort();
    this.update({status: 'cancelled', code: 'cancelled', result: '', message: '已取消分析，未生成有效报告。不会自动重试。', finishedAt: this.now()});
  }
}
