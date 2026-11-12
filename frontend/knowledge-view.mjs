import {KnowledgeTask, listDocuments, getDocument, getChunks, searchKnowledge, knowledgeTypes, scoreLabels} from './knowledge.mjs';
import {renderAnswer} from './render.mjs';

const statuses = {indexing: '正在入库', ready: '已发布', failed: '最新入库失败', cleanup_pending: '已发布 · 清理待重试'};
const create = (tag, text, className = '') => { const n = document.createElement(tag); n.textContent = text; n.className = className; return n; };
export function initKnowledgeView() {
  const $ = id => document.getElementById(id);
  let page = 1, selected = null, chunkPage = 1, citation = null;
  const filters = () => ({docType: $('kb-type').value, domain: $('kb-domain').value.trim(), service: $('kb-service').value.trim(),
    tags: $('kb-tags').value.split(/[,，]/).map(v => v.trim()).filter(Boolean), alertName: $('kb-alert').value.trim()});
  const message = (node, task, empty) => {
    node.textContent = task.status === 'loading' ? '正在加载…' : ['failed', 'cancelled'].includes(task.status) ? task.error : empty;
    node.classList.toggle('error', task.status === 'failed');
  };
  const list = new KnowledgeTask(renderList), detail = new KnowledgeTask(renderDetail), chunks = new KnowledgeTask(renderChunks), search = new KnowledgeTask(renderSearch);
  function loadList() { return list.run(signal => listDocuments({filters: filters(), q: $('kb-title-filter').value.trim(), page, signal})); }
  async function openDocument(id, hit = null) {
    citation = hit; selected = null; chunks.reset(); $('kb-chunks-area').hidden = true;
    const data = await detail.run(signal => getDocument(id, {signal}));
    if (!data) return;
    if (hit && data.activeVersion === hit.version) {
      $('kb-chunks-area').open = true; chunkPage = Math.floor((hit.chunkIndex || 0) / 20) + 1; await loadChunks();
    } else if (data.indexOnly) { $('kb-chunks-area').open = true; chunkPage = 1; await loadChunks(); }
  }
  function loadChunks() {
    if (!selected) return;
    const id = selected.id;
    return chunks.run(signal => getChunks(id, {page: chunkPage, signal}));
  }
  function renderList(task) {
    $('kb-list').setAttribute('aria-busy', String(task.status === 'loading'));
    const data = task.data;
    message($('kb-list-status'), task, data ? `${data.total} 篇文档` : '目录将在这里显示。');
    $('kb-list').replaceChildren();
    for (const d of data?.items || []) {
      const button = create('button', '', 'kb-document' + (selected?.id === d.id ? ' selected' : ''));
      button.type = 'button'; button.append(create('strong', d.title), create('span', `${knowledgeTypes[d.docType]}${d.service ? ' · ' + d.service : ''}`),
        create('small', `${d.chunkCount} 个片段 · ${statuses[d.status]} · ${new Date(d.updatedAt).toLocaleDateString('zh-CN')}`));
      button.addEventListener('click', () => void openDocument(d.id)); $('kb-list').append(button);
    }
    if (task.status === 'completed' && !data?.items.length) $('kb-list').append(create('p', '暂无匹配文档。可以调整筛选条件或上传知识文件。', 'kb-empty'));
    $('kb-page').textContent = data ? `${data.page} / ${Math.max(1, Math.ceil(data.total / data.pageSize))}` : '1 / 1';
    $('kb-prev').disabled = task.status === 'loading' || page <= 1;
    $('kb-next').disabled = task.status === 'loading' || !data || page * data.pageSize >= data.total;
  }
  function renderDetail(task) {
    message($('kb-detail-status'), task, task.data ? '' : '选择文档查看正文，或通过检索定位知识片段。');
    $('kb-detail').setAttribute('aria-busy', String(task.status === 'loading'));
    if (task.status !== 'completed' || !task.data) return;
    selected = task.data;
    const d = selected;
    $('kb-document-title').textContent = d.title;
    $('kb-document-meta').textContent = `${knowledgeTypes[d.docType]} · ${statuses[d.status]} · ${d.chunkCount} 个片段`;
    $('kb-document-source').textContent = `来源：${d.source}${d.service ? ' · 服务：' + d.service : ''}${d.domain ? ' · 领域：' + d.domain : ''}`;
    $('kb-document-tags').textContent = [...(d.tags || []), ...(d.aliases || []), ...(d.alertNames || [])].join(' · ');
    $('kb-document-version').textContent = `当前版本：${d.activeVersion ? d.activeVersion.slice(0, 12) : '尚未发布'} · 更新于 ${new Date(d.updatedAt).toLocaleString('zh-CN')}`;
    $('kb-document-link').replaceChildren();
    if (d.sourceUrl) { try { const url = new URL(d.sourceUrl); if (['http:', 'https:'].includes(url.protocol)) { const a = create('a', '打开原始来源'); a.href = url.href; a.target = '_blank'; a.rel = 'noopener noreferrer'; $('kb-document-link').append(a); } } catch {} }
    $('kb-document-notice').textContent = d.indexOnly ? '原始文件缺失，当前仅能查看索引片段。' : d.status === 'failed' ? '最新入库失败，当前展示已发布版本；可以重新上传文件重试。' : citation && citation.version !== d.activeVersion ? '文档已更新，当前展示最新发布版本。' : '';
    $('kb-document-notice').hidden = !$('kb-document-notice').textContent;
    renderAnswer($('kb-body'), d.content || '');
    $('kb-document-content').hidden = false; $('kb-chunks-area').hidden = false;
    renderList(list.state);
  }
  function renderChunks(task) {
    message($('kb-chunk-status'), task, task.data ? `${task.data.total} 个片段` : '展开后加载切片。');
    $('kb-chunks').replaceChildren();
    for (const c of task.data?.items || []) {
      const item = create('article', '', 'kb-chunk'); item.dataset.chunkId = c.id; item.tabIndex = -1;
      item.append(create('h4', `${c.index + 1}. ${c.sectionPath || '正文'}`), create('small', c.id, 'kb-chunk-id'));
      const body = create('div', '', 'message-body'); renderAnswer(body, c.content); item.append(body); $('kb-chunks').append(item);
      if (citation?.id === c.id) { item.classList.add('kb-highlight'); requestAnimationFrame(() => { item.scrollIntoView({block: 'center'}); item.focus({preventScroll: true}); }); }
    }
    const data = task.data;
    $('kb-chunk-page').textContent = data ? `${data.page} / ${Math.max(1, Math.ceil(data.total / data.pageSize))}` : '1 / 1';
    $('kb-chunk-prev').disabled = task.status === 'loading' || chunkPage <= 1;
    $('kb-chunk-next').disabled = task.status === 'loading' || !data || chunkPage * data.pageSize >= data.total;
  }
  function renderSearch(task) {
    $('kb-search-submit').disabled = task.status === 'loading'; $('kb-search-cancel').hidden = task.status !== 'loading';
    $('kb-results-area').hidden = task.status === 'idle';
    const data = task.data;
    message($('kb-search-status'), task, data ? `${data.documents.length} 个片段 · ${scoreLabels[data.scoreType]}${Number.isFinite(data.durationMs) ? ' · ' + data.durationMs + ' ms' : ''}` : '');
    $('kb-results').replaceChildren();
    if (task.status === 'completed' && !data?.documents.length) $('kb-results').append(create('p', '没有匹配的已发布知识。可以调整查询或过滤条件。', 'kb-empty'));
    for (const hit of data?.documents || []) {
      const item = create('article', '', 'kb-result');
      const button = create('button', hit.title || hit.source, 'kb-result-title'); button.type = 'button'; button.addEventListener('click', () => void openDocument(hit.documentId, hit));
      item.append(button, create('span', `${scoreLabels[hit.scoreType]} ${hit.score.toFixed(4)}`, 'kb-score'), create('small', `${hit.source}${hit.sectionPath ? ' / ' + hit.sectionPath : ''}`));
      const body = create('div', '', 'message-body'); renderAnswer(body, hit.content); item.append(body); $('kb-results').append(item);
    }
  }
  $('kb-search').addEventListener('submit', event => { event.preventDefault(); void search.run(signal => searchKnowledge({query: $('kb-query').value, mode: $('kb-mode').value, topK: Number($('kb-top-k').value), filters: filters(), signal})); });
  $('kb-search-cancel').addEventListener('click', () => search.cancel());
  $('kb-filters').addEventListener('submit', event => { event.preventDefault(); page = 1; search.reset(); void loadList(); });
  $('kb-filter-reset').addEventListener('click', () => { $('kb-filters').reset(); page = 1; search.reset(); void loadList(); });
  $('kb-refresh').addEventListener('click', () => void loadList());
  $('kb-prev').addEventListener('click', () => { page--; void loadList(); });
  $('kb-next').addEventListener('click', () => { page++; void loadList(); });
  $('kb-detail-reload').addEventListener('click', () => { if (selected) void openDocument(selected.id); });
  $('kb-chunks-area').addEventListener('toggle', () => { if ($('kb-chunks-area').open && !chunks.state.data && chunks.state.status !== 'loading' && selected) { chunkPage = 1; void loadChunks(); } });
  $('kb-chunk-prev').addEventListener('click', () => { chunkPage--; void loadChunks(); });
  $('kb-chunk-next').addEventListener('click', () => { chunkPage++; void loadChunks(); });
  renderList(list.state); renderDetail(detail.state); renderSearch(search.state); renderChunks(chunks.state);
  return {enter() { void loadList(); }, leave() { for (const task of [list, detail, chunks, search]) task.cancel(); },
    getSelected: () => selected, refresh: loadList, openDocument};
}
