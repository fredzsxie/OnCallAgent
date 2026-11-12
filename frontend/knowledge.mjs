import {responseJSON} from './api.mjs';

export const knowledgeTypes = {glossary: '专业术语', sop: '告警 SOP', reference: '参考资料'};
export const scoreLabels = {cosine: '相似度', bm25: 'BM25 得分', rrf: '融合得分'};
const validID = value => typeof value === 'string' && /^[a-f0-9]{64}$/.test(value);
export function validDocument(d) {
  return d && validID(d.id) && typeof d.source === 'string' && typeof d.title === 'string' &&
    Object.hasOwn(knowledgeTypes, d.docType) && typeof d.activeVersion === 'string' &&
    typeof d.latestVersion === 'string' && typeof d.updatedAt === 'string' &&
    Number.isSafeInteger(d.chunkCount) && d.chunkCount >= 0 && typeof d.indexOnly === 'boolean' &&
    ['indexing', 'ready', 'failed', 'cleanup_pending'].includes(d.status);
}
async function request(url, options, fetcher) {
  const response = await fetcher(url, options);
  try { return await responseJSON(response); }
  catch (err) {
    err.status = response.status;
    if (response.status === 409) err.message = '来源已存在，或文档版本已变化。请重新加载文档并核对后重试。';
    if (response.status === 404) err.message = '文档不存在，请刷新目录。';
    throw err;
  }
}
function checkPage(data, itemCheck) {
  if (!data || !Array.isArray(data.items) || !data.items.every(itemCheck) ||
      !Number.isSafeInteger(data.total) || data.total < 0 || !Number.isSafeInteger(data.page) || data.page < 1 ||
      !Number.isSafeInteger(data.pageSize) || data.pageSize < 1 || data.pageSize > 100) throw new Error('服务返回了无效的知识目录。');
  return data;
}
export async function listDocuments({filters = {}, q = '', page = 1, pageSize = 20, signal, fetcher = fetch} = {}) {
  const params = new URLSearchParams({page, pageSize});
  for (const [key, value] of Object.entries({...filters, q})) {
    if (Array.isArray(value)) { for (const item of value) params.append(`${key}[]`, item); }
    else if (value) params.set(key, value);
  }
  return checkPage(await request(`/api/knowledge/documents?${params}`, {signal}, fetcher), validDocument);
}
export async function getDocument(id, {signal, fetcher = fetch} = {}) {
  if (!validID(id)) throw new Error('文档标识无效。');
  const data = await request(`/api/knowledge/documents/${id}`, {signal}, fetcher);
  if (!validDocument(data) || (data.content !== undefined && typeof data.content !== 'string')) throw new Error('服务返回了无效的文档。');
  return data;
}
export async function getChunks(id, {page = 1, pageSize = 20, signal, fetcher = fetch} = {}) {
  if (!validID(id)) throw new Error('文档标识无效。');
  return checkPage(await request(`/api/knowledge/documents/${id}/chunks?page=${page}&pageSize=${pageSize}`, {signal}, fetcher),
    c => c && validID(c.id) && typeof c.content === 'string' && typeof c.sectionPath === 'string' && Number.isSafeInteger(c.index) && c.index >= 0);
}
export async function searchKnowledge({query, mode = 'hybrid', topK = 10, filters = {}, signal, fetcher = fetch}) {
  if (!query?.trim() || !['dense', 'bm25', 'hybrid'].includes(mode) || !Number.isSafeInteger(topK) || topK < 1 || topK > 100) throw new Error('请输入查询内容，并选择有效的模式和 TopK（1–100）。');
  const data = await request('/api/knowledge/search', {method: 'POST', headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({query: query.trim(), mode, topK, filters}), signal}, fetcher);
  if (!data || data.mode !== mode || data.scoreType !== {dense: 'cosine', bm25: 'bm25', hybrid: 'rrf'}[mode] ||
      !Array.isArray(data.documents) || !data.documents.every(h => h && validID(h.id) && validID(h.documentId) &&
        typeof h.version === 'string' && typeof h.content === 'string' && typeof h.title === 'string' &&
        typeof h.source === 'string' && typeof h.sectionPath === 'string' && Number.isFinite(h.score) && h.scoreType === data.scoreType)) throw new Error('服务返回了无效的检索结果。');
  return data;
}

export class KnowledgeTask {
  constructor(onChange = () => {}) { this.onChange = onChange; this.active = null; this.state = {status: 'idle', data: null, error: '', statusCode: 0}; }
  update(patch) { this.state = {...this.state, ...patch}; this.onChange(this.state); }
  async run(work) {
    this.active?.controller.abort();
    const op = {controller: new AbortController()}; this.active = op;
    this.update({status: 'loading', error: '', statusCode: 0});
    try {
      const data = await work(op.controller.signal);
      // A cancelled or superseded response cannot change the current document or search.
      if (this.active !== op) return;
      this.active = null; this.update({status: 'completed', data}); return data;
    } catch (err) {
      if (this.active !== op) return;
      this.active = null;
      this.update({status: op.controller.signal.aborted ? 'cancelled' : 'failed',
        error: err instanceof TypeError ? '网络连接失败，请检查连接后重试。' : err.message, statusCode: err.status || 0});
    }
  }
  cancel() {
    const op = this.active; if (!op) return;
    this.active = null; op.controller.abort(); this.update({status: 'cancelled', error: '已取消请求。'});
  }
  reset() { this.cancel(); this.update({status: 'idle', data: null, error: '', statusCode: 0}); }
}
