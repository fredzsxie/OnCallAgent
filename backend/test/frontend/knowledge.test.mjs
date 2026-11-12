import test from 'node:test';
import assert from 'node:assert/strict';
import {KnowledgeTask, listDocuments, getDocument, searchKnowledge} from '../../../frontend/knowledge.mjs';

const id = 'a'.repeat(64), version = 'b'.repeat(64);
const doc = {id, source: 'cdn.md', title: '回源', docType: 'glossary', activeVersion: version, latestVersion: version,
  updatedAt: '2026-11-12T00:00:00Z', chunkCount: 1, indexOnly: false, status: 'ready'};
const response = data => new Response(JSON.stringify({message: 'OK', data}), {headers: {'Content-Type': 'application/json'}});

test('knowledge directory preserves filters and validates pagination and document shape', async () => {
  const data = await listDocuments({filters: {docType: 'glossary', tags: ['a&b', '中文']}, q: 'origin & 回源', page: 2,
    fetcher: async url => {
      const params = new URL(url, 'http://localhost').searchParams;
      assert.equal(params.get('q'), 'origin & 回源'); assert.deepEqual(params.getAll('tags[]'), ['a&b', '中文']);
      assert.equal(params.get('page'), '2'); return response({items: [doc], total: 21, page: 2, pageSize: 20});
    }});
  assert.equal(data.items[0].title, '回源');
  await assert.rejects(getDocument(id, {fetcher: async () => response({...doc, id: '../secret'})}), /无效/);
  await assert.rejects(getDocument(id, {fetcher: async () => new Response(JSON.stringify({message: 'missing', data: null}),
    {status: 404, headers: {'Content-Type': 'application/json'}})}), err => err.status === 404);
});

test('hybrid results retain small RRF scores and reject mismatched score units', async () => {
  const hit = {id, documentId: id, version, content: '从源站获取内容', title: '回源', source: 'cdn.md', sectionPath: '回源', score: .01, scoreType: 'rrf'};
  const fetcher = async (url, init) => {
    assert.equal(url, '/api/knowledge/search'); assert.equal(JSON.parse(init.body).mode, 'hybrid');
    return response({mode: 'hybrid', scoreType: 'rrf', documents: [hit]});
  };
  const result = await searchKnowledge({query: 'origin', fetcher}); assert.equal(result.documents[0].score, .01);
  await assert.rejects(searchKnowledge({query: 'origin', fetcher: async () => response({mode: 'hybrid', scoreType: 'cosine', documents: []})}), /无效/);
  await assert.rejects(searchKnowledge({query: 'origin', topK: 101, fetcher: () => assert.fail('invalid query reached fetch')}), /TopK/);
});

test('cancelled and superseded knowledge requests cannot replace newer state', async () => {
  const task = new KnowledgeTask(); let resolveOld, rejectOld;
  const first = task.run(signal => new Promise((resolve, reject) => { resolveOld = resolve; rejectOld = reject; signal.addEventListener('abort', () => {}); }));
  await task.run(async () => ({id: 'new'})); resolveOld({id: 'old'}); await first;
  assert.equal(task.state.data.id, 'new'); assert.equal(task.state.status, 'completed');
  const pending = task.run(() => new Promise((resolve, reject) => { rejectOld = reject; }));
  task.cancel(); await task.run(async () => ({id: 'latest'})); rejectOld(new Error('late failure')); await pending;
  assert.equal(task.state.data.id, 'latest'); assert.equal(task.state.status, 'completed');
});

test('upload uses multipart metadata and freezes the expected version for updates', async () => {
  const {saveDocument} = await import('../../../frontend/knowledge.mjs');
  const file = new File(['# 回源\n定义'], 'origin.md', {type: 'text/markdown'});
  const created = await saveDocument({file, metadata: {docType: 'glossary'}, source: 'cdn/origin.md', fetcher: async (url, init) => {
    assert.equal(url, '/api/knowledge/documents'); assert.equal(init.method, 'POST'); assert.equal(init.body.get('source'), 'cdn/origin.md');
    assert.equal(JSON.parse(init.body.get('metadata')).docType, 'glossary'); assert.equal(init.body.get('file').name, 'origin.md'); return response(doc);
  }}); assert.equal(created.id, id);
  await saveDocument({file, document: doc, metadata: {aliases: []}, fetcher: async (url, init) => {
    assert.equal(url, `/api/knowledge/documents/${id}`); assert.equal(init.method, 'PUT'); assert.equal(init.body.get('expectedVersion'), version);
    assert.deepEqual(JSON.parse(init.body.get('metadata')).aliases, []); return response(doc);
  }});
  await assert.rejects(saveDocument({file, document: doc, fetcher: async () => new Response(JSON.stringify({message: 'conflict', data: null}),
    {status: 409, headers: {'Content-Type': 'application/json'}})}), err => err.status === 409 && err.message.includes('重新加载'));
  await assert.rejects(saveDocument({file, maxBytes: 1, fetcher: () => assert.fail('oversize upload reached fetch')}), /超过/);
});
