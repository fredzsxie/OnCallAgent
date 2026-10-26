import {readEvents, ProtocolError} from './sse.mjs';

export async function responseJSON(response) {
  if (!response.headers.get('content-type')?.toLowerCase().includes('application/json')) {
    throw new Error(`服务返回了无法识别的响应（HTTP ${response.status}）。`);
  }
  let result;
  try { result = await response.json(); } catch { throw new Error('服务返回了无效 JSON。'); }
  if (!response.ok) {
    const stage = result?.data?.stage;
    const label = stage === 'save' ? '文件保存失败' : stage === 'ingest' ? '知识入库失败' : '请求失败';
    throw new Error(`${label}（HTTP ${response.status}）：${typeof result?.message === 'string' ? result.message : '请稍后重试'}`);
  }
  if (result?.message !== 'OK' || !result.data || typeof result.data !== 'object') throw new Error('服务返回了无效的数据结构。');
  return result.data;
}

export async function chat({id, question, stream, signal, onConnected, onContent, fetcher = fetch}) {
  const response = await fetcher(stream ? '/api/chat_stream' : '/api/chat', {
    method: 'POST', headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({Id: id, Question: question}), signal,
  });
  if (!stream || !response.ok) {
    const data = await responseJSON(response);
    if (typeof data.answer !== 'string' || !data.answer.trim()) throw new ProtocolError('服务没有返回有效回答。');
    return data.answer;
  }
  if (!response.headers.get('content-type')?.toLowerCase().startsWith('text/event-stream')) {
    await response.body?.cancel();
    throw new ProtocolError('服务未返回 SSE 消息流。');
  }
  return readEvents(response.body, {signal, onConnected, onContent});
}

export function validateFile(file, maxBytes) {
  if (!file || !/\.(txt|md|markdown)$/i.test(file.name)) throw new Error('请选择 txt、md 或 markdown 文件。');
  if (file.size === 0) throw new Error('不能上传空文件。');
  if (file.size > maxBytes) throw new Error(`文件超过前端限制（${formatBytes(maxBytes)}）；服务端仍会进行最终校验。`);
}

export function formatBytes(bytes) {
  return bytes >= 1048576 ? `${(bytes / 1048576).toFixed(1)} MiB` : `${bytes} B`;
}

export async function upload(file, {signal, maxBytes, fetcher = fetch}) {
  validateFile(file, maxBytes);
  const body = new FormData();
  body.append('file', file);
  const response = await fetcher('/api/upload', {method: 'POST', body, signal});
  const data = await responseJSON(response);
  if (typeof data.fileName !== 'string' || typeof data.source !== 'string' ||
      !Number.isSafeInteger(data.fileSize) || data.fileSize <= 0 ||
      !Number.isSafeInteger(data.chunkCount) || data.chunkCount <= 0) throw new Error('服务没有返回有效入库结果。');
  return data;
}
