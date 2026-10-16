import {chat, upload, validateFile, formatBytes} from './api.mjs';
import {loadHistory, saveHistory, newSession, newID} from './history.mjs';
import {renderAnswer} from './render.mjs';

const $ = id => document.getElementById(id);
const warn = text => { $('storage-warning').textContent = text; $('storage-warning').hidden = false; };
let storage;
try { storage = window.localStorage; } catch { storage = {getItem() {throw new Error();}, setItem() {throw new Error();}}; }
const state = loadHistory(storage, warn);
let active = state.sessions.find(s => s.id === state.activeId);
let operation = null;
const configuredMax = window.ONCALL_CONFIG?.uploadMaxBytes;
const uploadMax = Number.isSafeInteger(configuredMax) && configuredMax > 0 ? configuredMax : 10485760;
const nodes = new Map();
const persist = () => saveHistory(storage, state, warn);
const dateText = timestamp => new Date(timestamp).toLocaleString('zh-CN', {month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit'});
const nearBottom = () => $('transcript').scrollHeight - $('transcript').scrollTop - $('transcript').clientHeight < 100;
const scrollBottom = () => { $('transcript').scrollTop = $('transcript').scrollHeight; $('jump-bottom').hidden = true; };
const contextHint = () => { $('context-warning').hidden = !active.messages.length; };

function newChat() {
  if (operation) return;
  active = newSession();
  state.sessions.unshift(active);
  state.activeId = active.id;
  $('question').value = '';
  $('context-warning').hidden = true;
  $('upload-status').hidden = true;
  persist();
  renderConversation();
  closeMenu();
  $('question').focus();
}

function renderSessions() {
  $('sessions').replaceChildren();
  $('session-count').textContent = state.sessions.length;
  for (const session of [...state.sessions].sort((a, b) => b.updatedAt - a.updatedAt)) {
    const button = document.createElement('button');
    button.className = 'session' + (session.id === state.activeId ? ' active' : '');
    button.disabled = !!operation;
    button.setAttribute('aria-current', String(session.id === state.activeId));
    const title = document.createElement('span');
    title.className = 'session-title'; title.textContent = session.title;
    const date = document.createElement('span');
    date.className = 'session-date'; date.textContent = dateText(session.updatedAt);
    button.append(title, date);
    button.addEventListener('click', () => {
      if (operation) return;
      active = session; state.activeId = session.id;
      $('question').value = ''; $('upload-status').hidden = true;
      contextHint(); persist(); renderConversation(); closeMenu();
    });
    $('sessions').append(button);
  }
}

function messageNode(message) {
  const article = document.createElement('article');
  const header = document.createElement('div'); header.className = 'message-header';
  const avatar = document.createElement('span'); avatar.className = 'avatar'; avatar.textContent = message.role === 'user' ? '你' : 'O';
  const role = document.createElement('span'); role.className = 'message-role'; role.textContent = message.role === 'user' ? '你' : 'OnCallAgent';
  const status = document.createElement('span'); status.className = 'message-state';
  const body = document.createElement('div'); body.className = 'message-body';
  if (message.role === 'user') body.classList.add('plain-text');
  const error = document.createElement('p'); error.className = 'message-error';
  const retry = document.createElement('button'); retry.className = 'retry-button'; retry.textContent = '重新发送此问题';
  retry.addEventListener('click', () => {
    if (operation) return;
    const index = active.messages.indexOf(message);
    $('question').value = active.messages[index - 1]?.content || '';
    $('question').focus();
  });
  header.append(avatar, role, status); article.append(header, body, error, retry);
  nodes.set(message.id, {article, body, status, error, retry});
  updateMessage(message);
  return article;
}

function updateMessage(message) {
  const view = nodes.get(message.id);
  view.article.className = `message ${message.role} ${message.status}`;
  view.status.textContent = message.status === 'completed' ? '' : message.status === 'pending' ? '处理中' : message.status === 'cancelled' ? '未完成 · 已停止' : '未完成';
  if (message.role === 'assistant') renderAnswer(view.body, message.content);
  else view.body.textContent = message.content;
  view.error.textContent = message.error || '';
  view.error.hidden = !message.error;
  view.retry.hidden = message.role !== 'assistant' || ['completed', 'pending'].includes(message.status);
  view.retry.disabled = !!operation;
}

function renderConversation() {
  nodes.clear(); $('messages').replaceChildren();
  $('conversation-title').textContent = active.title;
  $('welcome').hidden = active.messages.length > 0;
  for (const message of active.messages) $('messages').append(messageNode(message));
  renderSessions(); scrollBottom(); setBusy();
}

function setBusy() {
  const busy = !!operation;
  for (const id of ['new-chat', 'clear-history', 'send', 'upload', 'mode', 'question']) $(id).disabled = busy;
  $('send').hidden = busy;
  $('stop').hidden = !busy || operation.kind !== 'chat';
  if (!busy) $('activity').textContent = '准备就绪';
  $('composer').setAttribute('aria-busy', String(busy));
  renderSessions();
  for (const view of nodes.values()) view.retry.disabled = busy;
  for (const button of document.querySelectorAll('[data-question]')) button.disabled = busy;
}

function finish(op) {
  if (operation !== op) return;
  operation = null;
  setBusy(); persist();
}

async function send(event) {
  event?.preventDefault();
  if (operation) return;
  const question = $('question').value.trim();
  if (!question) return;
  if (active.lastSuccessAt && Date.now() - active.lastSuccessAt >= 30 * 60 * 1000) contextHint();
  const user = {id: newID(), role: 'user', content: question, status: 'pending'};
  const answer = {id: newID(), role: 'assistant', content: '', status: 'pending'};
  active.messages.push(user, answer); active.updatedAt = Date.now();
  if (active.messages.length === 2) active.title = question.slice(0, 32);
  const stream = $('mode').value === 'stream';
  const op = {kind: 'chat', controller: new AbortController(), user, answer};
  operation = op;
  $('question').value = ''; renderConversation(); persist();
  $('activity').textContent = '正在连接…';
  try {
    const result = await chat({id: active.id, question, stream, signal: op.controller.signal,
      onConnected: () => { if (operation === op) $('activity').textContent = '正在检索或生成…'; },
      onContent: content => {
        // A cancelled request must never update a new request or a different conversation.
        if (operation !== op) return;
        const follow = nearBottom();
        answer.content += content; updateMessage(answer);
        $('activity').textContent = '正在接收回答…';
        persist();
        if (follow) scrollBottom(); else $('jump-bottom').hidden = false;
      },
    });
    if (operation !== op) return;
    answer.content = result; answer.status = user.status = 'completed';
    active.lastSuccessAt = active.updatedAt = Date.now();
  } catch (err) {
    if (operation !== op) return;
    answer.status = user.status = op.controller.signal.aborted ? 'cancelled' : 'failed';
    answer.error = op.controller.signal.aborted ? '已停止。本轮结果未确认；再次提问时可补充必要背景。' : `${err instanceof TypeError ? '网络请求失败，请检查服务连接。' : err.message} 未确认完成的回答不会作为成功记录。网络中断时无法确认服务端是否已提交；重新发送可能产生重复轮次。`;
  } finally {
    if (operation === op) {
      const follow = nearBottom();
      updateMessage(user); updateMessage(answer); finish(op);
      if (follow) scrollBottom();
      $('question').focus();
    }
  }
}

function stop() {
  const op = operation;
  if (!op || op.kind !== 'chat') return;
  op.controller.abort();
  op.user.status = op.answer.status = 'cancelled';
  op.answer.error = '已停止，回答未完成。网络中断或停止时无法确认服务端是否已提交本轮；不会自动重试。';
  updateMessage(op.user); updateMessage(op.answer);
  finish(op); $('question').focus();
}

async function selectFile() {
  const file = $('file').files[0]; $('file').value = '';
  if (!file || operation) return;
  const status = $('upload-status'); status.hidden = false; status.classList.remove('error');
  try { validateFile(file, uploadMax); } catch (err) { status.textContent = err.message; status.classList.add('error'); return; }
  const op = {kind: 'upload', controller: new AbortController()};
  operation = op; setBusy(); $('activity').textContent = '正在入库…';
  status.textContent = `正在保存并入库 ${file.name}，请稍候…`;
  try {
    const result = await upload(file, {signal: op.controller.signal, maxBytes: uploadMax});
    if (operation !== op) return;
    status.textContent = `已入库：${result.fileName} · ${formatBytes(result.fileSize)} · ${result.chunkCount} 个片段。来源：${result.source}。现在可以提问；文件可供所有会话检索。`;
  } catch (err) {
    if (operation !== op) return;
    status.textContent = `${err instanceof TypeError ? '网络连接失败，入库结果未确认。' : err.message} 可重新选择同一文件重试。`;
    status.classList.add('error');
  } finally { finish(op); }
}

const mobile = matchMedia('(max-width:760px)');
function syncMenu() { $('sidebar').inert = mobile.matches && !$('sidebar').classList.contains('open'); }
function closeMenu() { $('sidebar').classList.remove('open'); $('menu-toggle').setAttribute('aria-expanded', 'false'); syncMenu(); }
$('composer').addEventListener('submit', send);
$('question').addEventListener('keydown', event => {
  if (event.key === 'Enter' && !event.shiftKey && !event.isComposing && event.keyCode !== 229) { event.preventDefault(); void send(); }
});
$('stop').addEventListener('click', stop);
$('new-chat').addEventListener('click', newChat);
$('upload').addEventListener('click', () => $('file').click());
$('upload').title = `txt / md / markdown，前端上限 ${formatBytes(uploadMax)}，以服务端校验为准`;
$('file').addEventListener('change', selectFile);
$('mode').addEventListener('change', () => { $('mode-label').textContent = $('mode').selectedOptions[0].textContent; });
$('clear-history').addEventListener('click', () => {
  if (operation || !confirm('仅清除当前浏览器中的会话记录，不会删除后端上下文或知识库文件。是否继续？')) return;
  state.sessions = []; state.activeId = null; newChat();
});
$('menu-toggle').addEventListener('click', () => {
  const open = $('sidebar').classList.toggle('open'); $('menu-toggle').setAttribute('aria-expanded', String(open)); syncMenu();
});
mobile.addEventListener('change', syncMenu);
syncMenu();
$('transcript').addEventListener('click', closeMenu);
$('transcript').addEventListener('scroll', () => { $('jump-bottom').hidden = nearBottom(); });
$('jump-bottom').addEventListener('click', scrollBottom);
for (const button of document.querySelectorAll('[data-question]')) button.addEventListener('click', () => { $('question').value = button.dataset.question; $('question').focus(); });
window.addEventListener('pagehide', () => { operation?.controller.abort(); persist(); });
window.addEventListener('pageshow', event => {
  if (event.persisted && operation?.kind === 'chat') stop();
});
window.addEventListener('focus', () => { if (active?.lastSuccessAt && Date.now() - active.lastSuccessAt >= 30 * 60 * 1000) contextHint(); });
if (active) { contextHint(); renderConversation(); persist(); }
else newChat();
