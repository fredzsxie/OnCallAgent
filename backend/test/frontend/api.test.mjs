import {test} from 'node:test';
import assert from 'node:assert/strict';
import {chat, upload, validateFile, responseJSON} from '../../../frontend/api.mjs';

const json = (data, status = 200) => new Response(JSON.stringify(data), {status, headers: {'Content-Type': 'application/json'}});

test('both chat modes send exactly Id/Question and propagate cancellation', async () => {
  for (const stream of [false, true]) {
    const controller = new AbortController();
    let captured;
    const answer = await chat({id: 'same-Id', question: '多行\n内容', stream, signal: controller.signal, fetcher: async (url, options) => {
      captured = options;
      assert.equal(url, stream ? '/api/chat_stream' : '/api/chat');
      if (!stream) return json({message: 'OK', data: {answer: '回答'}});
      return new Response('event: connected\ndata: {"status":"connected"}\n\nevent: message\ndata: {"content":"回答"}\n\nevent: done\ndata: {"status":"completed"}\n\n', {headers: {'Content-Type': 'text/event-stream; charset=utf-8'}});
    }});
    assert.equal(answer, '回答'); assert.equal(captured.signal, controller.signal);
    assert.deepEqual(JSON.parse(captured.body), {Id: 'same-Id', Question: '多行\n内容'});
  }
  const controller = new AbortController();
  const waiting = chat({id: 'a', question: 'q', stream: false, signal: controller.signal,
    fetcher: (_url, {signal}) => new Promise((_resolve, reject) => signal.addEventListener('abort', () => reject(new DOMException('cancelled', 'AbortError'))))});
  controller.abort(); await assert.rejects(waiting, {name: 'AbortError'});
});

test('HTTP errors, HTML proxy responses and invalid success data remain errors', async () => {
  await assert.rejects(responseJSON(new Response('<script>private</script>', {status: 502, headers: {'Content-Type': 'text/html'}})), /HTTP 502/);
  await assert.rejects(responseJSON(json({message: 'failed', data: {stage: 'save'}}, 500)), /文件保存失败/);
  await assert.rejects(responseJSON(json({message: 'failed', data: {stage: 'ingest'}}, 503)), /知识入库失败/);
  await assert.rejects(chat({id: 'a', question: 'q', stream: true, fetcher: async () => json({message: 'model request timed out', data: null}, 504)}), /504/);
  await assert.rejects(chat({id: 'a', question: 'q', stream: true, fetcher: async () => json({message: 'OK', data: {answer: 'a'}})}), /SSE/);
  for (const data of [{}, {answer: ''}, {answer: 1}]) await assert.rejects(chat({id: 'a', question: 'q', fetcher: async () => json({message: 'OK', data})}));
});

test('upload validates file types and size boundaries then sends actual multipart data', async () => {
  for (const name of ['guide.txt', 'guide.md', 'guide.MARKDOWN']) validateFile({name, size: 10}, 10);
  for (const file of [{name: 'guide.pdf', size: 1}, {name: 'empty.md', size: 0}, {name: 'large.md', size: 11}]) assert.throws(() => validateFile(file, 10));
  const file = new File(['# Runbook'], 'guide.md', {type: 'text/markdown'});
  const controller = new AbortController(); let calls = 0;
  const result = await upload(file, {signal: controller.signal, maxBytes: 100, fetcher: async (url, options) => {
    calls++; assert.equal(url, '/api/upload'); assert.equal(options.signal, controller.signal);
    assert.equal(options.headers, undefined); assert.equal(options.body.get('file').name, 'guide.md');
    assert.equal(await options.body.get('file').text(), '# Runbook');
    return json({message: 'OK', data: {fileName: 'guide-hash.md', source: 'guide.md', fileSize: file.size, chunkCount: 1}});
  }});
  assert.equal(result.chunkCount, 1); assert.equal(calls, 1);
  await assert.rejects(upload(new File([], 'empty.md'), {maxBytes: 100, fetcher: () => {calls++;}}));
  assert.equal(calls, 1);
});
