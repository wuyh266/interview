import { useEffect, useLayoutEffect, useRef, useState } from "react";
import {
  AppWindow, Calculator, CircleAlert, History, Layers3, ListTodo, LoaderCircle,
  MessageSquare, PanelLeft, Plus, Search, Send, X,
} from "lucide-react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { useWorkspace } from "./use-workspace.js";

const examples = [
  {
    title: "精确计算", detail: "大数和小数运算", icon: Calculator,
    prompt: "请计算 12345678901234567890 × 0.125。",
  },
  {
    title: "查询资料", detail: "检索本地模拟资料", icon: Search,
    prompt: "请查找 Go Context 取消与超时的模拟资料。",
  },
  {
    title: "记录待办", detail: "保存到当前会话", icon: ListTodo,
    prompt: "请把「整理本周周报」记为待办。",
  },
];

const markdownComponents = {
  a: ({ node, ...props }) => <a {...props} target="_blank" rel="noreferrer" />,
  table: ({ node, ...props }) => <div className="table-scroll"><table {...props} /></div>,
};

function AgentMark({ small = false }) {
  return (
    <span className={small ? "agent-mark agent-mark--small" : "agent-mark"} aria-hidden="true">
      <Layers3 size={small ? 17 : 21} strokeWidth={1.9} />
    </span>
  );
}

function Sidebar({ activeId, sessions, open, closeRef, onClose, onNew, onSelect }) {
  return (
    <aside id="session-sidebar" className={`sidebar${open ? " sidebar--open" : ""}`} aria-label="会话侧栏">
      <div className="brand">
        <AgentMark />
        <span>Agent <strong>工作台</strong></span>
        <button ref={closeRef} className="icon-button sidebar-close" aria-label="关闭会话侧栏" onClick={onClose}>
          <X size={20} />
        </button>
      </div>

      <button className="new-chat" onClick={onNew}>
        <Plus size={19} aria-hidden="true" />
        新建会话
      </button>

      <nav className="session-nav" aria-label="会话列表">
        <h2>会话</h2>
        {sessions.length === 0 ? (
          <p className="session-empty">发送消息后，会话将显示在这里。</p>
        ) : (
          <ul>
            {sessions.map(({ id, title }) => (
              <li key={id}>
                <button
                  className={`session-item${activeId === id ? " session-item--active" : ""}`}
                  aria-current={activeId === id ? "page" : undefined}
                  title={title}
                  onClick={() => onSelect(id)}
                >
                  <MessageSquare size={17} aria-hidden="true" />
                  <span>{title}</span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </nav>

      <div className="sidebar-footer">
        <History size={16} aria-hidden="true" />
        <span>每个会话保留最近 20 轮</span>
      </div>
    </aside>
  );
}

function Welcome({ onExample }) {
  return (
    <div className="welcome content-width">
      <div className="welcome-heading">
        <div>
          <div className="welcome-rule" aria-hidden="true" />
          <h2>今天想做什么？</h2>
          <p>计算、查找资料，或记录一条待办。</p>
        </div>
        <div className="workspace-graphic" aria-hidden="true">
          <div className="graphic-outline" />
          <div className="graphic-block graphic-block--accent" />
          <div className="graphic-block graphic-block--ink" />
          <div className="graphic-line graphic-line--first" />
          <div className="graphic-line graphic-line--second" />
        </div>
      </div>
      <div className="example-row" aria-label="示例任务">
        {examples.map(({ title, detail, prompt, icon: Icon }) => (
          <button key={title} className="example" onClick={() => onExample(prompt)}>
            <span className="example-icon"><Icon size={21} aria-hidden="true" /></span>
            <span className="example-copy"><strong>{title}</strong><span>{detail}</span></span>
          </button>
        ))}
      </div>
    </div>
  );
}

function HistoryStatus({ error, onNew, onRetry }) {
  if (!error) {
    return <div className="history-status" role="status"><LoaderCircle className="spinner" size={20} /> 正在恢复会话…</div>;
  }
  return (
    <div className="history-status history-status--error" role="alert">
      <CircleAlert size={24} aria-hidden="true" />
      <p>{error.message}</p>
      <button className="secondary-button" onClick={error.missing ? onNew : onRetry}>
        {error.missing ? "新建会话" : "重试"}
      </button>
    </div>
  );
}

function Conversation({ turns, pending }) {
  return (
    <div className="conversation content-width" role="log" aria-label="对话记录">
      {turns?.map((turn, index) => (
        <div className="turn" key={index}>
          <div className="user-message" aria-label="你的消息">{turn.user_input}</div>
          <div className="agent-message" aria-label="Agent 回复">
            <AgentMark small />
            <div className="reply">
              <Markdown skipHtml remarkPlugins={[remarkGfm]} components={markdownComponents}>{turn.answer}</Markdown>
            </div>
          </div>
        </div>
      ))}
      {pending && (
        <div className="turn">
          <div className="user-message" aria-label="你的消息">{pending}</div>
          <div className="agent-message" role="status">
            <AgentMark small />
            <span className="pending-message"><LoaderCircle className="spinner" size={17} /> 正在生成回复…</span>
          </div>
        </div>
      )}
    </div>
  );
}

function Composer({ thread, canSend, inputRef, setDraft, submit }) {
  useLayoutEffect(() => {
    const input = inputRef.current;
    input.style.height = "auto";
    input.style.height = `${Math.min(input.scrollHeight, 144)}px`;
  }, [inputRef, thread.draft]);

  function onKeyDown(event) {
    if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing && event.keyCode !== 229) {
      event.preventDefault();
      submit();
    }
  }

  return (
    <div className="composer-wrap">
      <div className="content-width">
        {thread.sendError && (
          <p className="send-error" role="alert"><CircleAlert size={17} aria-hidden="true" /> {thread.sendError}</p>
        )}
        <form className="composer" onSubmit={(event) => { event.preventDefault(); submit(); }}>
          <textarea
            ref={inputRef}
            rows={1}
            aria-label="消息"
            placeholder="输入你的问题…"
            disabled={Boolean(thread.loadError)}
            value={thread.draft}
            onChange={(event) => setDraft(event.target.value)}
            onKeyDown={onKeyDown}
          />
          <button className="send-button" type="submit" disabled={!canSend || !thread.draft.trim() || Boolean(thread.pending)} aria-label="发送消息">
            <Send size={18} aria-hidden="true" />
            <span>发送</span>
          </button>
        </form>
        <div className="composer-help"><kbd>Enter</kbd> 发送 <span>·</span> <kbd>Shift + Enter</kbd> 换行</div>
      </div>
    </div>
  );
}

export function App() {
  const { activeId, sessions, selectSession, thread, canSend, setDraft, submit, retryRestore } = useWorkspace();
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const messagesRef = useRef(null);
  const inputRef = useRef(null);
  const menuRef = useRef(null);
  const closeRef = useRef(null);
  const activeTitle = sessions.find(({ id }) => id === activeId)?.title ?? (activeId ? "会话" : "新会话");
  const loading = Boolean(activeId && thread.turns === null && !thread.loadError && !thread.pending);
  const empty = !loading && !thread.loadError && !thread.turns?.length && !thread.pending;

  useEffect(() => {
    const pane = messagesRef.current;
    pane.scrollTop = pane.scrollHeight;
  }, [activeId, thread.turns, thread.pending]);

  useEffect(() => {
    if (!sidebarOpen) return;
    closeRef.current?.focus();
    function onEscape(event) {
      if (event.key === "Escape") {
        setSidebarOpen(false);
        menuRef.current?.focus();
      }
    }
    window.addEventListener("keydown", onEscape);
    return () => window.removeEventListener("keydown", onEscape);
  }, [sidebarOpen]);

  function focusComposer() {
    requestAnimationFrame(() => inputRef.current?.focus());
  }

  function startNew() {
    selectSession(null);
    setSidebarOpen(false);
    focusComposer();
  }

  function closeSidebar() {
    setSidebarOpen(false);
    menuRef.current?.focus();
  }

  function chooseSession(id) {
    selectSession(id);
    if (sidebarOpen) closeSidebar();
  }

  function useExample(prompt) {
    setDraft(prompt);
    focusComposer();
  }

  return (
    <div className="workspace">
      {sidebarOpen && <button className="sidebar-backdrop" aria-label="关闭会话侧栏" tabIndex={-1} onClick={closeSidebar} />}
      <Sidebar
        activeId={activeId}
        sessions={sessions}
        open={sidebarOpen}
        closeRef={closeRef}
        onClose={closeSidebar}
        onNew={startNew}
        onSelect={chooseSession}
      />

      <main className="workspace-main" aria-label="Agent 对话">
        <header className="topbar">
          <div className="topbar-title">
            <button
              ref={menuRef}
              className="icon-button menu-toggle"
              aria-label="打开会话侧栏"
              aria-controls="session-sidebar"
              aria-expanded={sidebarOpen}
              onClick={() => setSidebarOpen(true)}
            ><PanelLeft size={21} /></button>
            <MessageSquare className="topbar-icon" size={18} aria-hidden="true" />
            <h1>{activeTitle}</h1>
          </div>
          <a className="window-link" href={window.location.pathname} target="_blank" rel="noopener" aria-label="另开窗口">
            <AppWindow size={18} aria-hidden="true" />
            <span>另开窗口</span>
          </a>
        </header>

        <section ref={messagesRef} className="conversation-scroll" aria-label="会话内容">
          {loading || thread.loadError ? (
            <HistoryStatus error={thread.loadError} onNew={startNew} onRetry={retryRestore} />
          ) : empty ? (
            <Welcome onExample={useExample} />
          ) : (
            <Conversation turns={thread.turns} pending={thread.pending} />
          )}
        </section>

        <Composer thread={thread} canSend={canSend} inputRef={inputRef} setDraft={setDraft} submit={submit} />
      </main>
    </div>
  );
}
