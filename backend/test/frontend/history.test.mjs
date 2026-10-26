import {test} from 'node:test';
import assert from 'node:assert/strict';
import {loadHistory, saveHistory, newSession, STORAGE_KEY} from '../../../frontend/history.mjs';

function memory() {
  const map = new Map();
  return {getItem: key => map.get(key), setItem: (key, value) => map.set(key, value)};
}

test('versioned history preserves Id, full text and success time; pending output becomes interrupted', () => {
  const storage = memory(); const warnings = [];
  const session = newSession(100);
  session.lastSuccessAt = 150;
  session.messages.push({id: 'u', role: 'user', content: '中文\n问题', status: 'completed'}, {id: 'a', role: 'assistant', content: 'partial', status: 'pending'});
  const state = {activeId: session.id, sessions: [session]};
  assert.equal(saveHistory(storage, state, w => warnings.push(w)), true);
  const restored = loadHistory(storage, w => warnings.push(w));
  assert.equal(restored.activeId, session.id);
  assert.equal(restored.sessions[0].lastSuccessAt, 150);
  assert.equal(restored.sessions[0].messages[1].status, 'interrupted');
  assert.equal(restored.sessions[0].messages[0].content, '中文\n问题');
  assert.equal(session.messages[1].status, 'pending');
  assert.deepEqual(warnings, []);
  assert.notEqual(newSession().id, session.id);
});

test('invalid versions, unsafe shapes and corrupt storage degrade without throwing', () => {
  for (const raw of ['bad', 'null', '{"version":2,"sessions":[]}', JSON.stringify({version: 1, activeId: 'x', sessions: [{id: 'x'}]})]) {
    const warnings = []; const storage = memory(); storage.setItem(STORAGE_KEY, raw);
    assert.deepEqual(loadHistory(storage, w => warnings.push(w)), {sessions: [], activeId: null});
    assert.equal(warnings.length, 1); assert.equal(storage.getItem(STORAGE_KEY), raw);
  }
  const warnings = [];
  const blocked = {getItem() {throw new Error('denied');}, setItem() {throw new Error('quota');}};
  assert.deepEqual(loadHistory(blocked, w => warnings.push(w)), {sessions: [], activeId: null});
  assert.equal(saveHistory(blocked, {sessions: []}, w => warnings.push(w)), false);
  assert.equal(warnings.length, 2);
});
