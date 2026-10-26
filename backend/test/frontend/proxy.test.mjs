import {test} from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import {once} from 'node:events';
import {createFrontendServer} from '../../../frontend/server.mjs';

async function listen(server, t) {
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => {server.closeAllConnections(); return new Promise(resolve => server.close(resolve));});
  return `http://127.0.0.1:${server.address().port}`;
}

test('static allowlist exposes UI assets and never repository files', async t => {
  const url = await listen(createFrontendServer(), t);
  for (const name of ['/', '/app.js', '/sse.mjs', '/styles.css', '/vendor/purify-3.4.17.min.js', '/vendor/marked-18.1.1.umd.js']) {
    const res = await fetch(url + name); assert.equal(res.status, 200);
    assert.match(res.headers.get('content-security-policy'), /script-src 'self'/);
    await res.arrayBuffer();
  }
  for (const name of ['/.env', '/backend/.env', '/server.mjs', '/README.md', '/test/frontend/x', '/%2e%2e/backend/.env']) {
    const res = await fetch(url + name); assert.equal(res.status, 404); await res.arrayBuffer();
  }
});

test('proxy preserves JSON, multipart bytes, query and response status', async t => {
  const upstream = http.createServer(async (req, res) => {
    const chunks = []; for await (const chunk of req) chunks.push(chunk);
    res.writeHead(201, {'Content-Type': 'application/json', 'X-Fixture': 'actual-upstream'});
    res.end(JSON.stringify({path: req.url, type: req.headers['content-type'], body: Buffer.concat(chunks).toString()}));
  });
  const target = await listen(upstream, t);
  const url = await listen(createFrontendServer({apiOrigin: target}), t);
  const chat = await fetch(url + '/api/chat?example=1', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: '{"Id":"a","Question":"中文"}'});
  assert.equal(chat.status, 201); assert.equal(chat.headers.get('x-fixture'), 'actual-upstream');
  assert.deepEqual(await chat.json(), {path: '/api/chat?example=1', type: 'application/json', body: '{"Id":"a","Question":"中文"}'});
  const form = new FormData(); form.append('file', new File(['# guide'], 'guide.md'));
  const response = await fetch(url + '/api/upload', {method: 'POST', body: form});
  const result = await response.json();
  assert.match(result.type, /multipart\/form-data; boundary=/); assert.match(result.body, /filename="guide.md"/); assert.match(result.body, /# guide/);
});

test('SSE headers and first event arrive before completion; abort closes upstream', async t => {
  let closed;
  const disconnected = new Promise(resolve => {closed = resolve;});
  const target = await listen(http.createServer((req, res) => {
    res.writeHead(200, {'Content-Type': 'text/event-stream', 'X-Accel-Buffering': 'no'});
    res.write('event: connected\ndata: {"status":"connected"}\n\n');
    res.on('close', closed);
  }), t);
  const url = await listen(createFrontendServer({apiOrigin: target}), t);
  const controller = new AbortController();
  const response = await fetch(url + '/api/chat_stream', {method: 'POST', body: '{}', signal: controller.signal});
  assert.equal(response.headers.get('x-accel-buffering'), 'no');
  const reader = response.body.getReader();
  const first = await reader.read(); assert.match(new TextDecoder().decode(first.value), /event: connected/);
  controller.abort();
  await assert.rejects(reader.read());
  await Promise.race([disconnected, new Promise((_r, reject) => {const timer = setTimeout(() => reject(new Error('upstream leaked')), 2000); timer.unref();})]);
});

test('unreachable API returns safe JSON and origins reject credentials or paths', async t => {
  const closedServer = http.createServer(); closedServer.listen(0, '127.0.0.1'); await once(closedServer, 'listening');
  const port = closedServer.address().port; await new Promise(resolve => closedServer.close(resolve));
  const url = await listen(createFrontendServer({apiOrigin: `http://127.0.0.1:${port}`}), t);
  const response = await fetch(url + '/api/chat', {method: 'POST', body: '{}'});
  assert.equal(response.status, 502); assert.deepEqual(await response.json(), {message: 'API connection failed', data: null});
  for (const origin of ['file:///tmp', 'http://user:secret@localhost', 'http://localhost/v1', 'http://localhost?key=secret']) assert.throws(() => createFrontendServer({apiOrigin: origin}));
});

test('proxy budget is configurable and an ops timeout returns safe JSON', async t => {
  for (const value of [0, -1, '', NaN, Infinity]) assert.throws(() => createFrontendServer({apiProxyTimeoutMs: value}));
  const target = await listen(http.createServer((req, res) => {req.resume();}), t);
  const url = await listen(createFrontendServer({apiOrigin: target, apiProxyTimeoutMs: 30}), t);
  const response = await fetch(url + '/api/ai_ops', {method: 'POST', body: '{}'});
  assert.equal(response.status, 504); assert.deepEqual(await response.json(), {message: 'API proxy timed out', data: null});
});
