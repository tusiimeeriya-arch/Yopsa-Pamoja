const keyFor = (userId) => `pamoja:drafts:${userId}`;

export function listDrafts(userId) {
  if (!userId) return [];
  try { return JSON.parse(localStorage.getItem(keyFor(userId)) || '[]'); } catch { return []; }
}

export function saveDraft(userId, draft) {
  const list = listDrafts(userId);
  const item = {
    id: draft.id || crypto.randomUUID(),
    idempotencyKey: draft.idempotencyKey || crypto.randomUUID(),
    createdAt: new Date().toISOString(),
    status: 'pending',
    ...draft,
  };
  localStorage.setItem(keyFor(userId), JSON.stringify([item, ...list]));
  window.dispatchEvent(new CustomEvent('pamoja:drafts'));
  return item;
}

export function updateDraft(userId, id, patch) {
  const list = listDrafts(userId).map((d) => (d.id === id ? { ...d, ...patch } : d));
  localStorage.setItem(keyFor(userId), JSON.stringify(list));
  window.dispatchEvent(new CustomEvent('pamoja:drafts'));
}

export function removeDraft(userId, id) {
  localStorage.setItem(keyFor(userId), JSON.stringify(listDrafts(userId).filter((d) => d.id !== id)));
  window.dispatchEvent(new CustomEvent('pamoja:drafts'));
}

export function clearDrafts(userId) {
  if (userId) localStorage.removeItem(keyFor(userId));
  window.dispatchEvent(new CustomEvent('pamoja:drafts'));
}

export function clearAllLocalData() {
  for (const k of Object.keys(localStorage)) if (k.startsWith('pamoja:')) localStorage.removeItem(k);
  sessionStorage.clear();
}
