export class ProtocolError extends Error {}

export class EventParser {
  constructor() {
    this.buffer = '';
    this.name = '';
    this.data = [];
  }

  push(text) {
    this.buffer += text;
    const events = [];
    let start = 0;
    // A trailing CR may be the first half of CRLF in the next network chunk.
    for (let i = 0; i < this.buffer.length; i++) {
      const char = this.buffer[i];
      if (char !== '\n' && char !== '\r') continue;
      if (char === '\r' && i === this.buffer.length - 1) break;
      const line = this.buffer.slice(start, i);
      if (char === '\r' && this.buffer[i + 1] === '\n') i++;
      start = i + 1;
      if (line === '') {
        if (this.data.length) events.push({event: this.name || 'message', data: this.data.join('\n')});
        this.name = '';
        this.data = [];
      } else if (!line.startsWith(':')) {
        const colon = line.indexOf(':');
        const key = colon < 0 ? line : line.slice(0, colon);
        let value = colon < 0 ? '' : line.slice(colon + 1);
        if (value.startsWith(' ')) value = value.slice(1);
        if (key === 'event') this.name = value;
        if (key === 'data') this.data.push(value);
      }
    }
    this.buffer = this.buffer.slice(start);
    return events;
  }
}

export async function readEvents(body, {signal, onConnected = () => {}, onContent = () => {}} = {}) {
  if (!body) throw new ProtocolError('响应没有可读取的消息流。');
  const reader = body.getReader();
  const decoder = new TextDecoder('utf-8', {fatal: true});
  const parser = new EventParser();
  let connected = false;
  let done = false;
  let answer = '';
  const cancel = () => { void reader.cancel().catch(() => {}); };
  signal?.addEventListener('abort', cancel, {once: true});
  try {
    for (;;) {
      if (signal?.aborted) throw new DOMException('已停止生成', 'AbortError');
      const part = await reader.read();
      if (signal?.aborted) throw new DOMException('已停止生成', 'AbortError');
      const text = decoder.decode(part.value, {stream: !part.done});
      for (const event of parser.push(text)) {
        if (!['connected', 'message', 'error', 'done'].includes(event.event)) continue;
        if (done) throw new ProtocolError('结束事件后收到额外消息。');
        let data;
        try { data = JSON.parse(event.data); } catch { throw new ProtocolError('消息流包含无效 JSON。'); }
        if (!data || typeof data !== 'object' || Array.isArray(data)) throw new ProtocolError('消息流格式无效。');
        if (event.event === 'connected') {
          if (connected || data.status !== 'connected') throw new ProtocolError('连接事件格式无效。');
          connected = true;
          onConnected();
        } else {
          if (!connected) throw new ProtocolError('消息流缺少连接事件。');
          if (event.event === 'error') throw new ProtocolError(typeof data.message === 'string' ? data.message : '聊天执行失败。');
          if (event.event === 'message') {
            if (typeof data.content !== 'string') throw new ProtocolError('正文事件格式无效。');
            answer += data.content;
            onContent(data.content);
          }
          if (event.event === 'done') {
            if (data.status !== 'completed' || !answer.trim()) throw new ProtocolError('回答未完整结束。');
            done = true;
          }
        }
      }
      if (done) return answer;
      if (part.done) throw new ProtocolError('连接已结束，但未收到完成事件；本轮结果未确认。');
    }
  } finally {
    signal?.removeEventListener('abort', cancel);
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}
