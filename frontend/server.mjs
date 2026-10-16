import http from 'node:http';
import https from 'node:https';
import {readFile} from 'node:fs/promises';
import {fileURLToPath, pathToFileURL} from 'node:url';
import path from 'node:path';

const root = fileURLToPath(new URL('.', import.meta.url));
const files = new Set(['index.html', 'styles.css', 'app.js', 'config.js', 'api.mjs', 'sse.mjs', 'history.mjs', 'render.mjs', 'vendor/marked-18.1.1.umd.js', 'vendor/purify-3.4.17.min.js']);
const types = {'.html': 'text/html; charset=utf-8', '.css': 'text/css; charset=utf-8', '.js': 'text/javascript; charset=utf-8', '.mjs': 'text/javascript; charset=utf-8'};
const hopHeaders = ['connection', 'keep-alive', 'proxy-authenticate', 'proxy-authorization', 'te', 'trailer', 'transfer-encoding', 'upgrade'];

function headersWithoutHop(headers) {
  const copy = {...headers};
  const connectionTokens = String(copy.connection || '').split(',').map(s => s.trim().toLowerCase());
  for (const name of [...hopHeaders, ...connectionTokens]) delete copy[name];
  return copy;
}

function sendError(res, status, message) {
  if (res.headersSent) return res.destroy();
  res.writeHead(status, {'Content-Type': 'application/json; charset=utf-8', 'Cache-Control': 'no-store'});
  res.end(JSON.stringify({message, data: null}));
}

export function createFrontendServer({apiOrigin = 'http://127.0.0.1:6872'} = {}) {
  const target = new URL(apiOrigin);
  if (!['http:', 'https:'].includes(target.protocol) || target.username || target.password || target.search || target.hash || target.pathname !== '/') {
    throw new Error('ONCALL_API_ORIGIN must be an http(s) origin without path, credentials, query or fragment');
  }
  return http.createServer(async (req, res) => {
    let url;
    try { url = new URL(req.url, 'http://localhost'); } catch { return sendError(res, 400, 'invalid request'); }
    if (url.pathname === '/api' || url.pathname.startsWith('/api/')) {
      // Stream both directions: do not buffer uploads or SSE, and cancel upstream on disconnect.
      const outgoingHeaders = headersWithoutHop(req.headers);
      outgoingHeaders.host = target.host;
      delete outgoingHeaders['accept-encoding'];
      const transport = target.protocol === 'https:' ? https : http;
      const upstream = transport.request({protocol: target.protocol, hostname: target.hostname, port: target.port,
        method: req.method, path: url.pathname + url.search, headers: outgoingHeaders}, incoming => {
        const headers = headersWithoutHop(incoming.headers);
        res.writeHead(incoming.statusCode, headers);
        res.flushHeaders();
        incoming.on('error', () => res.destroy());
        incoming.on('close', () => { if (!incoming.complete) res.destroy(); });
        incoming.pipe(res);
      });
      upstream.setTimeout(120000, () => { sendError(res, 504, 'API proxy timed out'); upstream.destroy(); });
      upstream.on('error', () => sendError(res, 502, 'API connection failed'));
      req.on('aborted', () => upstream.destroy());
      req.on('error', () => upstream.destroy());
      res.on('close', () => { if (!res.writableFinished) upstream.destroy(); });
      req.pipe(upstream);
      return;
    }
    if (req.method !== 'GET' && req.method !== 'HEAD') return sendError(res, 405, 'method not allowed');
    const filename = url.pathname === '/' ? 'index.html' : url.pathname.slice(1);
    if (!files.has(filename)) return sendError(res, 404, 'not found');
    try {
      const contents = await readFile(path.join(root, filename));
      res.writeHead(200, {'Content-Type': types[path.extname(filename)], 'Content-Length': contents.length,
        'Cache-Control': 'no-store', 'X-Content-Type-Options': 'nosniff',
        'Content-Security-Policy': "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"});
      res.end(req.method === 'HEAD' ? undefined : contents);
    } catch { sendError(res, 500, 'static file unavailable'); }
  });
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  try {
    const value = process.env.ONCALL_FRONTEND_PORT ?? '8080';
    if (!/^\d+$/.test(value) || Number(value) < 1 || Number(value) > 65535) throw new Error('ONCALL_FRONTEND_PORT must be between 1 and 65535');
    const server = createFrontendServer({apiOrigin: process.env.ONCALL_API_ORIGIN ?? 'http://127.0.0.1:6872'});
    server.on('error', err => { console.error(`Frontend server failed: ${err.code || 'initialization error'}`); process.exitCode = 1; });
    server.listen(Number(value), '127.0.0.1', () => console.log(`OnCallAgent: http://127.0.0.1:${value}`));
    for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => {
      server.close(); server.closeAllConnections();
    });
  } catch (err) { console.error(err.message); process.exitCode = 1; }
}
