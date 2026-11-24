import { useCallback, useEffect, useRef, useState } from "react";
import { createSession, restoreSession, sendMessage } from "../session-api.mjs";

const sessionPrefix = "minimal-agent.session.";
const historyLimit = 20;
const blankThread = {
  turns: null,
  draft: "",
  pending: "",
  loading: false,
  loadError: null,
  sendError: "",
};

function currentSession() {
  const id = new URL(window.location.href).searchParams.get("session");
  return id && /^[\da-f]{32}$/i.test(id) ? id : null;
}

function storedSession(id) {
  try {
    const item = JSON.parse(window.localStorage.getItem(sessionPrefix + id));
    return typeof item?.title === "string" && Number.isFinite(item.updatedAt) ? item : null;
  } catch {
    return null;
  }
}

function readSessions() {
  try {
    return Object.keys(window.localStorage)
      .filter((key) => key.startsWith(sessionPrefix))
      .map((key) => key.slice(sessionPrefix.length))
      .flatMap((id) => {
        const item = storedSession(id);
        return item ? [{ ...item, id }] : [];
      })
      .sort((a, b) => b.updatedAt - a.updatedAt);
  } catch {
    return [];
  }
}

function conversationTitle(text) {
  const characters = Array.from(text.trim().replace(/\s+/g, " "));
  return characters.length > 40 ? `${characters.slice(0, 40).join("")}…` : characters.join("");
}

function errorText(error, restoring = false) {
	if (error.status === 409) return "会话刚被另一条请求更新，请重试。";
  if (error.status === 404 && restoring) return "这个会话已失效，请新建会话继续。";
  if (error.status === 504) return "等待回复超时，请重试。";
  if (error.status === 502) return "Agent 暂时无法完成这条消息，请重试。";
  if (error.status === 413) return "消息太长，请缩短后发送。";
  return "暂时无法连接会话服务，请确认 Go 后端已启动。";
}

export function useWorkspace() {
  const [activeId, setActiveId] = useState(currentSession);
  const active = useRef(activeId);
  const [sessions, setSessions] = useState(readSessions);
  const [threads, setThreads] = useState({});
  const [restoreAttempt, setRestoreAttempt] = useState(0);
  const sending = useRef(new Set());
  const restoreRequest = useRef(null);
  const thread = threads[activeId ?? ""] ?? blankThread;

  const updateThread = useCallback((id, update) => {
    const key = id ?? "";
    setThreads((previous) => ({
      ...previous,
      [key]: update(previous[key] ?? blankThread),
    }));
  }, []);

  const remember = useCallback((id, text) => {
    const existing = storedSession(id);
    const item = {
      id,
      title: (existing?.title !== "新会话" && existing?.title) || conversationTitle(text) || "新会话",
      updatedAt: Date.now(),
    };
    try {
      window.localStorage.setItem(sessionPrefix + id, JSON.stringify(item));
    } catch {
      // 浏览器关闭本地存储时，会话列表仍可在当前页面内使用。
    }
    setSessions((previous) => [item, ...previous.filter((entry) => entry.id !== id)]);
  }, []);

  const selectSession = useCallback((id) => {
    const url = new URL(window.location.href);
    if (id) url.searchParams.set("session", id);
    else url.searchParams.delete("session");
    if (url.href !== window.location.href) window.history.pushState(null, "", url);
    active.current = id;
    setActiveId(id);
  }, []);

  useEffect(() => {
    function onPopState() {
      active.current = currentSession();
      setActiveId(active.current);
    }
    function onStorage(event) {
      if (!event.key || event.key.startsWith(sessionPrefix)) setSessions(readSessions());
    }
    window.addEventListener("popstate", onPopState);
    window.addEventListener("storage", onStorage);
    return () => {
      window.removeEventListener("popstate", onPopState);
      window.removeEventListener("storage", onStorage);
    };
  }, []);

  useEffect(() => {
    if (!activeId || sending.current.has(activeId)) return;

    const controller = new AbortController();
    restoreRequest.current = controller;
    updateThread(activeId, (previous) => ({ ...previous, loading: true, loadError: null }));
    restoreSession(activeId, controller.signal)
      .then(({ turns }) => {
        if (controller.signal.aborted) return;
        updateThread(activeId, (previous) => ({ ...previous, turns, loading: false }));
        remember(activeId, turns[0]?.user_input ?? "");
      })
      .catch((error) => {
        if (controller.signal.aborted) return;
        updateThread(activeId, (previous) => ({
          ...previous,
          loading: false,
          loadError: { missing: error.status === 404, message: errorText(error, true) },
        }));
      });
    return () => {
      controller.abort();
      if (restoreRequest.current === controller) restoreRequest.current = null;
    };
  }, [activeId, restoreAttempt, remember, updateThread]);

  const canSend = !activeId || (thread.turns !== null && !thread.loading && !thread.loadError);

  function setDraft(draft) {
    updateThread(activeId, (previous) => ({ ...previous, draft }));
  }

  async function submit() {
    const text = thread.draft.trim();
    const originId = activeId;
    const originKey = originId ?? "";
    if (!text || !canSend || active.current !== originId || sending.current.has(originKey)) return;

    let targetId = originId;
    restoreRequest.current?.abort();
    sending.current.add(originKey);
    updateThread(originId, (previous) => ({ ...previous, draft: "", pending: text, sendError: "" }));

    try {
      if (!targetId) {
        const created = await createSession();
        targetId = created.session_id;
        sending.current.add(targetId);
        const stayInNewSession = active.current === null;
        setThreads((previous) => {
          const draftThread = previous[""] ?? blankThread;
          return {
            ...previous,
            "": { ...draftThread, pending: "", draft: stayInNewSession ? "" : draftThread.draft },
            [targetId]: {
              ...blankThread,
              turns: created.turns,
              pending: text,
              draft: stayInNewSession ? draftThread.draft : "",
            },
          };
        });
        remember(targetId, text);
        if (stayInNewSession) selectSession(targetId);
      }

      const reply = await sendMessage(targetId, text);
      updateThread(targetId, (previous) => ({
        ...previous,
        turns: [...(previous.turns ?? []), { user_input: text, answer: reply.answer }].slice(-historyLimit),
        pending: "",
        loading: false,
      }));
      remember(targetId, text);
    } catch (error) {
      const missing = Boolean(targetId && error.status === 404);
      updateThread(targetId, (previous) => ({
        ...previous,
        pending: "",
        draft: previous.draft || text,
        sendError: missing ? "" : errorText(error),
        loadError: missing ? { missing, message: errorText(error, true) } : previous.loadError,
      }));
    } finally {
      sending.current.delete(originKey);
      if (targetId) sending.current.delete(targetId);
    }
  }

  return {
    activeId,
    sessions,
    selectSession,
    thread,
    canSend,
    setDraft,
    submit,
    retryRestore: () => setRestoreAttempt((attempt) => attempt + 1),
  };
}
