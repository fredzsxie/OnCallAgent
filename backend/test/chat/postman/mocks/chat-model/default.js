const http = require('http');

http.createServer((req, res) => {
  if (req.method !== 'POST' || req.url.split('?')[0] !== '/v1/chat/completions') {
    res.writeHead(501, {'Content-Type': 'application/json'});
    return res.end(JSON.stringify({error: {message: 'Endpoint not modeled'}}));
  }
  let body = req.body;
  if (typeof body === 'string') { try { body = JSON.parse(body); } catch { body = null; } }
  if (!body || !Array.isArray(body.messages) || !body.model) {
    res.writeHead(400, {'Content-Type': 'application/json'});
    return res.end(JSON.stringify({error: {message: 'Invalid completion request'}}));
  }
  const users = body.messages.filter(m => m.role === 'user');
  const question = users.at(-1)?.content || '';
  if (question === '__error__') {
    res.writeHead(503, {'Content-Type': 'application/json'});
    return res.end(JSON.stringify({error: {message: 'fixture-private-error', type: 'server_error'}}));
  }
  const reply = () => {
    if (res.destroyed) return;
    let answer = 'first\nsecond "quoted" \\path';
    if (question.startsWith('remember:')) answer = question.slice('remember:'.length);
    if (question === 'recall') answer = users.findLast(m => m.content.startsWith('remember:'))?.content.slice('remember:'.length) || 'unknown';
    if (question === '__docs__') answer = body.messages.find(m => m.role === 'system')?.content.split('<documents>')[1]?.split('</documents>')[0]?.trim() || '知识库为空';
    if (question === '__markdown__') answer = '# 验收标题\n\n**加粗** 与 [安全链接](https://example.com)\n\n| 项目 | 状态 |\n| --- | --- |\n| 服务 | 正常 |\n\n```sh\necho "hello"\n```\n\n<script>window.__xss = true</script><img src="https://example.com/track" onerror="window.__xss = true"><svg onload="window.__xss = true"></svg><iframe srcdoc="unsafe"></iframe><style>body{display:none}</style><a href="javascript:alert(1)">危险链接</a><form><input autofocus onfocus="window.__xss = true"></form>';
    if (!body.stream) {
      res.writeHead(200, {'Content-Type': 'application/json'});
      return res.end(JSON.stringify({id:'fixture', object:'chat.completion', model:body.model, choices:[{index:0, message:{role:'assistant',content:answer},finish_reason:'stop'}]}));
    }
    res.writeHead(200, {'Content-Type':'text/event-stream'});
    const deltas = [{role:'assistant',content:''},{content:answer.slice(0,3)},{content:answer.slice(3)}];
    for (const delta of deltas) res.write('data: '+JSON.stringify({id:'fixture',object:'chat.completion.chunk',model:body.model,choices:[{index:0,delta}]})+'\n\n');
    if (question === '__broken_stream__') return res.destroy();
    res.end('data: [DONE]\n\n');
  };
  if (question === '__timeout__') setTimeout(reply,1500);
  else if (question === '__slow__') {
    const timer = setTimeout(reply,15000);
    res.on('close', () => clearTimeout(timer));
  } else reply();
}).listen(process.env.PORT);
