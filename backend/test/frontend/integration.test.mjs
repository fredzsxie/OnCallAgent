import {test} from 'node:test';
import assert from 'node:assert/strict';
import {randomUUID} from 'node:crypto';
import {chat, upload} from '../../../frontend/api.mjs';

test('explicit cloud acceptance through the frontend proxy: upload, retrieval, memory and isolation', {
  skip: process.env.ONCALL_INTEGRATION !== '1', timeout: 240000,
}, async t => {
  const value = process.env.ONCALL_FRONTEND_INTEGRATION_URL;
  assert.ok(value, 'set ONCALL_FRONTEND_INTEGRATION_URL to the running frontend proxy');
  const origin = new URL(value);
  assert.ok(['127.0.0.1', 'localhost', '[::1]'].includes(origin.hostname), 'integration must target an explicitly started local frontend');
  assert.ok(['http:', 'https:'].includes(origin.protocol));
  const fetcher = (url, options) => fetch(new URL(url, origin), options);
  const id = `stage08-${randomUUID()}`;
  const marker = `ORBIT-${randomUUID().slice(0, 8)}`;
  const filename = `${id}.md`;
  const text = `# 第八步知识库验收 ${id}\n\n${id} 运维手册规定：认证服务恢复后，使用恢复校验码 ${marker} 验证健康检查。该校验码仅用于本次验收。\n`;
  const result = await upload(new File([text], filename, {type: 'text/markdown'}), {maxBytes: 10485760, fetcher});
  assert.equal(result.source, filename); assert.equal(result.fileSize, new TextEncoder().encode(text).length); assert.ok(result.chunkCount > 0);
  const duplicate = await upload(new File([text], filename), {maxBytes: 10485760, fetcher});
  assert.deepEqual(duplicate, result);
  t.diagnostic(JSON.stringify({session: id, source: result.source, fileName: result.fileName, fileSize: result.fileSize, chunkCount: result.chunkCount, marker, duplicateUpload: 'matched'}));
  const answer = await chat({id, question: `根据提供的资料，${id} 的恢复校验码是什么？只回复校验码和来源文件名，不要调用工具。`, stream: false, fetcher});
  assert.ok(answer.includes(marker), 'retrieval answer must contain the uploaded code');
  assert.ok(answer.includes(filename), 'retrieval answer must cite the uploaded source');
  // The next request contains no code: recall must come from the backend conversation context.
  const recall = await chat({id, question: '刚才查到的恢复校验码是什么？仅回复校验码，不要调用工具。', stream: true, fetcher});
  assert.ok(recall.includes(marker));
  const isolated = await chat({id: `${id}-isolated`, question: '我上一个问题中的恢复校验码是什么？只能根据本会话历史回答，不要检索或调用工具。不知道就回复不知道。', stream: false, fetcher});
  assert.ok(!isolated.includes(marker));
  t.diagnostic(JSON.stringify({session: id, source: result.source, fileName: result.fileName, fileSize: result.fileSize, chunkCount: result.chunkCount, marker, answer, recall, isolated}));
});
