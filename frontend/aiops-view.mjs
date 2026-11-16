const statuses = {started: '进行中', completed: '已完成', failed: '失败', cancelled: '已取消', interrupted: '已中断'};
const roles = {Planner: '规划', Executor: '执行', Replanner: '证据复核'};
const text = value => typeof value === 'string' || typeof value === 'number' ? String(value) : '';
const fieldsText = value => value && typeof value === 'object' && !Array.isArray(value) ? Object.entries(value).map(([k, v]) => `${k}: ${text(v)}`).join('\n') : '';

export function durationText(ms) {
  if (ms < 1000) return `${Math.max(0, Math.round(ms))} 毫秒`;
  if (ms < 60000) return `${(ms / 1000).toFixed(1)} 秒`;
  return `${Math.floor(ms / 60000)} 分 ${Math.floor(ms % 60000 / 1000)} 秒`;
}

export function resultFields(step) {
  const out = step.output || {}, fields = [];
  const add = (label, value) => { const v = text(value); if (v) fields.push([label, v]); };
  add('查询词', step.input?.query || out.query); add('来源', out.source);
  add('检索模式', out.mode); add('评分类型', out.scoreType);
  add('实际时间', out.timestamp); add('时区', out.timezone); add('Unix 时间戳', out.unix_seconds);
  if (Array.isArray(out.steps)) add('计划步骤', out.steps.map((s, i) => `${i + 1}. ${text(s)}`).join('\n'));
  if (step.error) {
    add('错误原因', step.error.message); add('错误码', step.error.code);
    add('是否可重试', step.error.retryable ? '可检查依赖后手动重试' : '否');
  }
  if (Number.isSafeInteger(step.count)) add('结果总数', step.count);
  if (Array.isArray(out.alerts)) out.alerts.forEach((alert, i) => {
    add(`告警 ${i + 1}`, [text(alert.labels?.alertname), text(alert.labels?.instance)].filter(Boolean).join(' · '));
    add('状态', alert.state); add('触发时间', alert.activeAt); add('当前值', alert.value);
    add('标签', fieldsText(alert.labels)); add('注释', fieldsText(alert.annotations));
  });
  if (Array.isArray(out.documents)) out.documents.forEach((doc, i) => {
    add(`文档 ${i + 1}`, doc.title || doc.source); add('文档来源', doc.source);
    add('章节', doc.sectionPath); add('版本', doc.version);
    add('相关度', `${typeof doc.score === 'number' && Number.isFinite(doc.score) ? Number(doc.score.toPrecision(6)) : text(doc.score)}${doc.scoreType ? ' · ' + text(doc.scoreType) : ''}`);
    add('命中片段', text(doc.excerpt) + (doc.truncated ? '\n（片段已截断）' : ''));
  });
  if (out.truncated) add('展示范围', '结果较多，仅展示前 20 条；结果总数为完整计数。');
  return fields;
}

export class OpsProgressView {
  constructor({details, list, count, current}) {
    this.details = details; this.list = list; this.count = count; this.current = current;
    this.document = list.ownerDocument; this.nodes = new Map(); this.runId = null; this.status = 'idle';
  }
  element(tag, className, value) {
    const node = this.document.createElement(tag); if (className) node.className = className;
    if (value !== undefined) node.textContent = value; return node;
  }
  create(step) {
    const item = this.element('li', 'ops-step'); item.dataset.stepId = step.stepId;
    const icon = this.element('span', 'ops-step-icon'); icon.setAttribute('aria-hidden', 'true');
    const body = this.element('div', 'ops-step-body'), header = this.element('div', 'ops-step-header');
    const title = this.element('strong', 'ops-step-title'), badge = this.element('span', 'ops-step-badge'), duration = this.element('time', 'ops-step-duration');
    const meta = this.element('p', 'ops-step-meta'), summary = this.element('p', 'ops-step-summary');
    const result = this.element('details', 'ops-tool-details'), resultTitle = this.element('summary', '', '查看步骤详情'), fields = this.element('dl', 'ops-result-fields');
    result.append(resultTitle, fields);
    const children = this.element('ol', 'ops-substeps'); children.setAttribute('aria-label', '本步骤的工具调用');
    header.append(title, badge, duration); body.append(meta, header, summary, result, children); item.append(icon, body);
    const node = {item, icon, title, badge, duration, meta, summary, result, fields, children};
    this.nodes.set(step.stepId, node); return node;
  }
  update(value) {
    if (this.runId !== value.runId) {
      this.nodes.clear(); this.list.replaceChildren(); this.runId = value.runId;
      this.details.open = value.status === 'running';
    }
    if (value.status !== this.status && value.status !== 'running') this.details.open = value.status !== 'completed';
    this.status = value.status;
    const steps = value.steps || [];
    this.details.hidden = !steps.length;
    const tools = steps.filter(s => s.type === 'tool').length;
    this.count.textContent = `${steps.length - tools} 步 · ${tools} 项工具`;
    const active = steps.filter(s => s.status === 'started');
    this.current.hidden = value.status !== 'running';
    this.current.textContent = active.length ? `当前：${active.filter(s => s.type !== 'tool').map(s => s.title).join(' · ')}${active.some(s => s.type === 'tool') ? ' / ' + active.filter(s => s.type === 'tool').map(s => s.title).join('、') : ''}` : '正在准备分析任务…';
    for (const step of steps) {
      const node = this.nodes.get(step.stepId) || this.create(step);
      const parent = this.nodes.get(step.parentStepId)?.children || this.list;
      if (node.item.parentNode !== parent) parent.append(node.item);
      node.item.dataset.status = step.status;
      node.icon.textContent = step.status === 'completed' ? '✓' : step.status === 'failed' ? '×' : ['cancelled', 'interrupted'].includes(step.status) ? '–' : '';
      node.title.textContent = step.title || step.summary;
      node.badge.textContent = statuses[step.status] || step.status;
      node.meta.textContent = `${step.iteration ? '第 ' + step.iteration + ' 轮' : '初始计划'} · ${roles[step.agent] || step.agent}${step.tool ? ' / ' + step.tool : ''}`;
      node.summary.textContent = step.status === 'interrupted' ? '连接中断，未确认本步骤是否完成。' : step.status === 'cancelled' ? '本步骤已停止，未生成完整结果。' : step.summary;
      node.summary.hidden = step.status === 'started' && step.summary === step.title;
      if (!node.initialized || node.event !== step.output || node.failure !== step.error || node.input !== step.input) {
        const fields = resultFields(step); node.fields.replaceChildren();
        for (const [label, content] of fields) node.fields.append(this.element('dt', '', label), this.element('dd', '', content));
        node.initialized = true; node.result.hidden = !fields.length; node.event = step.output; node.failure = step.error; node.input = step.input;
      }
      node.step = step;
    }
    this.tick(value.finishedAt ?? performance.now());
  }
  tick(now) {
    for (const node of this.nodes.values()) {
      const step = node.step;
      const ms = step.status === 'started' ? Math.max(0, now - step.receivedAt) : step.elapsedMs;
      node.duration.textContent = `${step.status === 'started' ? '已用 ' : '耗时 '}${durationText(ms)}`;
      node.duration.setAttribute('datetime', `PT${(ms / 1000).toFixed(3)}S`);
    }
  }
}
