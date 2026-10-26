import {test} from 'node:test';
import assert from 'node:assert/strict';
import {EventParser, readEvents} from '../../../frontend/sse.mjs';

const encoder = new TextEncoder();
const connected = 'event: connected\ndata: {"status":"connected"}\n\n';
const done = 'event: done\ndata: {"status":"completed"}\n\n';
const message = value => `event: message\ndata: ${JSON.stringify({content: value})}\n\n`;
function bytesStream(text, size = 1) {
  const bytes = encoder.encode(text);
  return new ReadableStream({start(controller) {
    for (let i = 0; i < bytes.length; i += size) controller.enqueue(bytes.slice(i, i + size));
    controller.close();
  }});
}

test('SSE survives every UTF-8 boundary, CRLF splits, comments and multiline data', async () => {
  const content = '中文🙂\n第二行 "引号" \\路径';
  const text = ': ping\r\nid: ignored\r\n' + connected.replaceAll('\n', '\r\n') +
    'event: message\r\ndata: {"content":\r\ndata: ' + JSON.stringify(content) + '}\r\n\r\n' + done;
  for (const size of [1, 2, 3, 17, 65536]) {
    const output = []; let calls = 0;
    assert.equal(await readEvents(bytesStream(text, size), {onConnected: () => calls++, onContent: c => output.push(c)}), content);
    assert.equal(calls, 1); assert.equal(output.join(''), content);
  }
});

test('parser dispatches only complete events and honors exact data whitespace', () => {
  const p = new EventParser();
  assert.deepEqual(p.push('event: message\ndata:  first'), []);
  assert.deepEqual(p.push('\ndata: second\n'), []);
  assert.deepEqual(p.push('\n'), [{event: 'message', data: ' first\nsecond'}]);
  assert.deepEqual(p.push('event: ignored\n\n'), []);
});

test('failure events, malformed data, empty text and unconfirmed EOF reject', async () => {
  const cases = [
    ['', /完成/], [connected, /完成/], [message('missing connected') + done, /连接/],
    [connected + 'event: error\ndata: {"message":"safe error"}\n\n', /safe error/],
    [connected + 'event: message\ndata: bad\n\n', /JSON/],
    [connected + 'event: message\ndata: null\n\n', /格式/],
    [connected + 'event: message\ndata: {"content":3}\n\n', /正文/],
    [connected + message(' ') + done, /完整/],
    [connected + message('partial'), /完成/],
    [connected + message('answer') + 'event: done\ndata: {"status":"wrong"}\n\n', /完整/],
    [connected + message('answer') + done + message('late'), /额外/],
  ];
  for (const [text, error] of cases) await assert.rejects(readEvents(bytesStream(text, 65536)), error);
});

test('abort cancels an outstanding read and releases its reader', async () => {
  let cancelled = 0;
  const controller = new AbortController();
  const body = new ReadableStream({start(c) {c.enqueue(encoder.encode(connected));}, cancel() {cancelled++;}});
  const pending = readEvents(body, {signal: controller.signal, onConnected: () => controller.abort()});
  await assert.rejects(pending, {name: 'AbortError'});
  assert.equal(cancelled, 1); assert.equal(body.locked, false);
});

test('network failures and successful terminal events close stream resources', async () => {
  const body = new ReadableStream({start(c) {c.error(new Error('network interrupted'));}});
  await assert.rejects(readEvents(body), /network interrupted/);
  assert.equal(body.locked, false);
  let cancelled = 0;
  const successful = new ReadableStream({start(c) {c.enqueue(encoder.encode(connected + message('answer') + done));}, cancel() {cancelled++;}});
  assert.equal(await readEvents(successful), 'answer');
  assert.equal(cancelled, 1); assert.equal(successful.locked, false);
});

test('partial content is exposed for display but error never confirms a successful answer', async () => {
  const output = [];
  await assert.rejects(readEvents(bytesStream(connected + message('partial\ntext') + 'event: error\ndata: {"message":"execution failed"}\n\n'), {onContent: c => output.push(c)}), /execution failed/);
  assert.deepEqual(output, ['partial\ntext']);
});
