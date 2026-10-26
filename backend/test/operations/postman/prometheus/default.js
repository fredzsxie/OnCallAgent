const http = require('http');
http.createServer((req, res) => {
  const scenario = req.url.split('/').filter(Boolean)[0];
  if (req.method !== 'GET' || !req.url.endsWith('/api/v1/alerts')) {
    res.writeHead(501, {'Content-Type':'application/json'});
    return res.end(JSON.stringify({status:'error', error:'Endpoint not modeled'}));
  }
  const reply = () => {
    if (res.destroyed) return;
    res.setHeader('Content-Type','application/json');
    if (scenario === 'auth') {res.writeHead(401); return res.end('{"error":"fixture-private-secret"}');}
    if (scenario === 'upstream') {res.writeHead(503); return res.end('{"error":"fixture-private-secret"}');}
    if (scenario === 'bad') return res.end('<html>fixture-private-secret</html>');
    const alerts = scenario === 'empty' ? [] : ['one','two'].map(instance => ({labels:{alertname:'HighCPU',instance},annotations:{summary:'fixture alert'},state:'firing',activeAt:'2026-10-19T00:00:00Z',value:'1'}));
    res.end(JSON.stringify({status:'success',data:{alerts}}));
  };
  if (scenario === 'slow') {const timer=setTimeout(reply,1000);res.on('close',()=>clearTimeout(timer));} else reply();
}).listen(process.env.PORT);
