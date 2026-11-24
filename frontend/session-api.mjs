const sessionsPath = "/api/sessions";

async function request(path, options = {}) {
  const response = await fetch(path, { credentials: "same-origin", ...options });
  if (!response.ok) {
    const body = await response.json().catch(() => null);
    const error = new Error(body?.error ?? `HTTP ${response.status}`);
    error.status = response.status;
    throw error;
  }
  return response.json();
}

function sessionPath(sessionId) {
  return `${sessionsPath}/${encodeURIComponent(sessionId)}`;
}

export function createSession(signal) {
  return request(sessionsPath, { method: "POST", signal });
}

export function restoreSession(sessionId, signal) {
  return request(sessionPath(sessionId), { signal });
}

export function sendMessage(sessionId, text, signal) {
  return request(`${sessionPath(sessionId)}/messages`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ text }),
    signal,
  });
}
