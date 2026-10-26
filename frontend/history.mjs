export const STORAGE_KEY = 'oncallagent.history.v1';
const roles = new Set(['user', 'assistant']);
const statuses = new Set(['pending', 'completed', 'failed', 'cancelled', 'interrupted']);

export function newID() {
  if (crypto.randomUUID) return crypto.randomUUID();
  // randomUUID requires a secure context; getRandomValues also supports HTTP deployments.
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  bytes[6] = (bytes[6] & 15) | 64;
  bytes[8] = (bytes[8] & 63) | 128;
  const hex = [...bytes].map(b => b.toString(16).padStart(2, '0')).join('');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

export function newSession(now = Date.now()) {
  return {id: newID(), title: '新会话', createdAt: now, updatedAt: now, lastSuccessAt: null, messages: []};
}

export function loadHistory(storage, warn) {
  try {
    const raw = storage.getItem(STORAGE_KEY);
    if (!raw) return {sessions: [], activeId: null};
    const saved = JSON.parse(raw);
    if (saved.version !== 1 || !Array.isArray(saved.sessions) || (saved.activeId !== null && typeof saved.activeId !== 'string')) throw new Error();
    const seen = new Set();
    for (const s of saved.sessions) {
      if (typeof s.id !== 'string' || !s.id || seen.has(s.id) || typeof s.title !== 'string' ||
          !Number.isFinite(s.createdAt) || !Number.isFinite(s.updatedAt) ||
          (s.lastSuccessAt !== null && !Number.isFinite(s.lastSuccessAt)) || !Array.isArray(s.messages)) throw new Error();
      seen.add(s.id);
      for (const m of s.messages) {
        if (typeof m.id !== 'string' || !roles.has(m.role) || typeof m.content !== 'string' || !statuses.has(m.status) ||
            (m.error !== undefined && typeof m.error !== 'string')) throw new Error();
        // Pending output survived a reload; it is display history, never a confirmed backend turn.
        if (m.status === 'pending') m.status = 'interrupted';
      }
    }
    return {sessions: saved.sessions, activeId: saved.activeId};
  } catch {
    warn('无法读取本地记录（数据损坏或浏览器限制）。当前会话仍可使用；后续保存可能覆盖旧记录。');
    return {sessions: [], activeId: null};
  }
}

export function saveHistory(storage, state, warn) {
  try {
    storage.setItem(STORAGE_KEY, JSON.stringify({version: 1, sessions: state.sessions, activeId: state.activeId}));
    return true;
  } catch {
    warn('本地记录保存失败，可能是浏览器限制或空间不足。当前页面仍可聊天，刷新后可能丢失记录。');
    return false;
  }
}
