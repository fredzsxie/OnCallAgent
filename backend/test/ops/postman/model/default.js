const http = require('http');

const report = {
  alerts: '查询完成；仅依据实际工具返回的告警。',
  evidence: '已检查启用的工具。此报告由本地协议模拟生成。',
  root_causes: '候选原因需要依据手册及日志继续确认。',
  recommendations: '遵循内部手册，不自动执行修复。',
  unconfirmed: '禁用、失败或无数据的工具均为证据限制；无告警不代表系统健康。'
};

http.createServer((req, res) => {
  if (req.method !== 'POST' || !req.url.endsWith('/chat/completions')) {
    res.writeHead(501, {'Content-Type':'application/json'});
    return res.end('{"error":"Endpoint not modeled"}');
  }
  let body=req.body;
  if (typeof body === 'string') { try { body=JSON.parse(body); } catch { body=null; } }
  if (!body || !Array.isArray(body.messages)) { res.writeHead(400); return res.end(); }
  const text = (body.messages || []).map(m => typeof m.content === 'string' ? m.content : '').join('\n');
  if (text.includes('ops:model-error')) {
    res.writeHead(503, {'Content-Type':'application/json'});
    return res.end('{"error":{"message":"fixture-private-secret"}}');
  }
  const tools=(body.tools || []).map(t=>t.function.name);
  let name, args, content;
  if (body.model === 'planner-id') {
    if (tools.length === 1) { name='Plan'; args={steps:['Inspect alerts and logs']}; }
    else if (text.includes('ops:plan-limit') || (text.includes('ops:replan') && !text.includes('Follow up on limitations'))) {
      name='Plan'; args={steps:['Follow up on limitations']};
    } else { name='Respond'; args=report; }
  } else {
    const seen=new Set((body.messages || []).flatMap(m=>(m.tool_calls || []).map(c=>c.function.name)));
    name = text.includes('ops:executor-limit') ? 'get_current_time' :
      ['query_prometheus_alerts','get_current_time','query_log'].find(n=>tools.includes(n) && !seen.has(n));
    if (name) args=name==='query_log' ? {query:'level:ERROR'} : {};
    else content='Evidence collected; retain failures and missing tools as limitations.';
  }
  const toolCall=name ? {id:'fixture-call',type:'function',function:{name,arguments:JSON.stringify(args)}} : undefined;
  if (body.stream) {
    res.writeHead(200, {'Content-Type':'text/event-stream'});
    const delta=toolCall ? {role:'assistant',tool_calls:[{index:0,...toolCall}]} : {role:'assistant',content};
    res.write(`data: ${JSON.stringify({id:'ops-fixture',choices:[{index:0,delta}]})}\n\n`);
    return res.end('data: [DONE]\n\n');
  }
  res.writeHead(200, {'Content-Type':'application/json'});
  const message=toolCall ? {role:'assistant',tool_calls:[toolCall]} : {role:'assistant',content};
  res.end(JSON.stringify({id:'ops-fixture',model:body.model,choices:[{index:0,message,finish_reason:toolCall?'tool_calls':'stop'}]}));
}).listen(process.env.PORT);
