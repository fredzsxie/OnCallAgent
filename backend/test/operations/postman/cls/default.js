const http = require('http');
const properties = Object.fromEntries(['Region','TopicId','Query'].map(name=>[name,{type:'string'}]));
for (const name of ['From','To','Limit']) properties[name]={type:'number'};
const inputSchema={type:'object',properties,required:['Region','Query','From','To']};
http.createServer((req,res)=>{
  if (req.method !== 'POST' || !req.url.endsWith('/mcp')) {res.writeHead(501,{'Content-Type':'application/json'});return res.end('{"error":"Endpoint not modeled"}');}
  let body=req.body;
  if (typeof body==='string') {try {body=JSON.parse(body);} catch {body=null;}}
  if (!body || body.jsonrpc!=='2.0') {res.writeHead(400);return res.end();}
  const scenario=req.url.split('/').filter(Boolean)[0];
  res.setHeader('Content-Type','application/json');
  if (scenario==='auth') {res.writeHead(403);return res.end('{"error":"fixture-private-secret"}');}
  const reply = result => res.end(JSON.stringify({jsonrpc:'2.0',id:body.id,result}));
  if (body.method==='initialize') return reply({protocolVersion:'2025-06-18',capabilities:{tools:{}},serverInfo:{name:'cls-fixture',version:'1'}});
  if (body.method==='notifications/initialized') {res.writeHead(202);return res.end();}
  if (body.method==='tools/list') return reply({tools:scenario==='schema'?[]:[{name:'SearchLog',description:'Official CLS SearchLog protocol fixture',inputSchema}]});
  if (body.method!=='tools/call' || body.params?.name!=='SearchLog') {res.writeHead(501);return res.end('{"error":"Endpoint not modeled"}');}
  const args=body.params.arguments;
  if (!args.Region || !args.TopicId || typeof args.Query!=='string' || !Number.isSafeInteger(args.From) || !Number.isSafeInteger(args.To) || args.From>=args.To || args.Limit<1 || args.Limit>1000) return reply({isError:true,content:[{type:'text',text:'invalid SearchLog arguments'}]});
  const result = () => {
    if (res.destroyed) return;
    if (args.Query==='__upstream__') return reply({isError:true,content:[{type:'text',text:'fixture-private-secret'}]});
    if (args.Query==='__bad__') return reply({content:[{type:'text',text:'[{...(truncated)'}]});
    if (args.Query==='__auth__') return reply({isError:true,content:[{type:'text',text:'{"code":"AuthFailure.SignatureFailure","message":"fixture-private-secret"}'}]});
    const records=args.Query==='__empty__'?[]:[{Time:args.From,Source:'fixture-host',LogJson:'{"level":"ERROR","message":"fixture recovery"}'}];
    if (args.Query==='__sql__') return reply({structuredContent:{records:[{count:3}]},content:[{type:'text',text:'do not use fallback'}]});
    reply({content:[{type:'text',text:JSON.stringify(records)}]});
  };
  if (args.Query==='__slow__') {const timer=setTimeout(result,1000);res.on('close',()=>clearTimeout(timer));} else result();
}).listen(process.env.PORT);
