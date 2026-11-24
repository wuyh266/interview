package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"

	"example.com/minimal-agent/internal/agent"
	"example.com/minimal-agent/internal/session"
)

const (
	sessionsPath   = "/api/sessions"
	browserCookie  = "agent_browser"
	sessionCookie  = "agent_session_owner"
	ownerCookieAge = 365 * 24 * 60 * 60
	tokenBytes     = 16
	maxMessageBody = 64 << 10
)

type sessionHandler struct {
	runtime *agent.Runtime
}

type turnView struct {
	UserInput string `json:"user_input"`
	Answer    string `json:"answer"`
}

type sessionView struct {
	SessionID string     `json:"session_id"`
	Turns     []turnView `json:"turns"`
}

type messageView struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id"`
	Answer    string `json:"answer"`
}

// NewHandler 提供会话创建、恢复和续聊的 HTTP 接口。
func NewHandler(runtime *agent.Runtime) http.Handler {
	h := sessionHandler{runtime: runtime}
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+sessionsPath, h.create)
	mux.HandleFunc("GET "+sessionsPath+"/{sessionID}", h.restore)
	mux.HandleFunc("POST "+sessionsPath+"/{sessionID}/messages", h.message)
	return mux
}

func (h sessionHandler) create(w http.ResponseWriter, r *http.Request) {
	owner := cookieToken(r, browserCookie)
	newBrowser := owner == ""
	if newBrowser {
		var err error
		owner, err = randomToken()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not create browser identity")
			return
		}
	}
	id, err := randomToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not generate session ID")
		return
	}
	key := session.SessionKey{UserID: ownerID(owner), SessionID: id}
	if err := h.runtime.CreateSession(r.Context(), key); err != nil {
		if errors.Is(err, session.ErrSessionExists) {
			writeError(w, http.StatusConflict, "session already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	path := sessionsPath + "/" + id
	if newBrowser {
		setOwnerCookie(w, r, browserCookie, owner, sessionsPath)
	}
	// 每个会话的凭据使用独立路径，首次并发建窗时也能分别恢复。
	setOwnerCookie(w, r, sessionCookie, owner, path)
	w.Header().Set("Location", path)
	writeJSON(w, http.StatusCreated, sessionView{SessionID: id, Turns: []turnView{}})
}

func (h sessionHandler) restore(w http.ResponseWriter, r *http.Request) {
	key, state, ok := h.load(w, r)
	if !ok {
		return
	}
	view := sessionView{SessionID: key.SessionID, Turns: make([]turnView, 0, len(state.History.Turns))}
	for _, turn := range state.History.Turns {
		view.Turns = append(view.Turns, turnView{UserInput: turn.UserInput, Answer: turn.Answer})
	}
	writeJSON(w, http.StatusOK, view)
}

func (h sessionHandler) message(w http.ResponseWriter, r *http.Request) {
	key, _, ok := h.load(w, r)
	if !ok {
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "expected application/json")
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxMessageBody))
	decoder.DisallowUnknownFields()
	var input struct {
		Text string `json:"text"`
	}
	if err := decoder.Decode(&input); err != nil {
		writeInvalidBody(w, err)
		return
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		writeInvalidBody(w, err)
		return
	}
	if strings.TrimSpace(input.Text) == "" {
		writeError(w, http.StatusBadRequest, "text is required")
		return
	}
	result, err := h.runtime.Run(r.Context(), agent.RunInput{Session: key, Text: input.Text})
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		}
		slog.ErrorContext(r.Context(), "agent message failed", "run_id", result.RunID, "error", err)
		if errors.Is(err, session.ErrSessionConflict) {
			writeError(w, http.StatusConflict, "session was updated, retry the message")
			return
		}
		if errors.Is(err, session.ErrContextLimit) {
			writeError(w, http.StatusUnprocessableEntity, "conversation context limit exceeded")
			return
		}
		writeError(w, status, "agent could not complete the message")
		return
	}
	writeJSON(w, http.StatusOK, messageView{SessionID: key.SessionID, RunID: result.RunID, Answer: result.Answer})
}

func (h sessionHandler) load(w http.ResponseWriter, r *http.Request) (session.SessionKey, session.Session, bool) {
	id := r.PathValue("sessionID")
	owner := cookieToken(r, sessionCookie)
	if owner == "" {
		owner = cookieToken(r, browserCookie)
	}
	if owner == "" || !validToken(id) {
		writeError(w, http.StatusNotFound, "session not found")
		return session.SessionKey{}, session.Session{}, false
	}
	key := session.SessionKey{UserID: ownerID(owner), SessionID: id}
	state, err := h.runtime.RestoreSession(r.Context(), key)
	if err != nil {
		if errors.Is(err, session.ErrSessionNotFound) {
			writeError(w, http.StatusNotFound, "session not found")
		} else {
			writeError(w, http.StatusInternalServerError, "could not load session")
		}
		return session.SessionKey{}, session.Session{}, false
	}
	return key, state, true
}

func randomToken() (string, error) {
	var id [tokenBytes]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}

func validToken(value string) bool {
	if len(value) != hex.EncodedLen(tokenBytes) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func cookieToken(r *http.Request, name string) string {
	cookie, err := r.Cookie(name)
	if err != nil || !validToken(cookie.Value) {
		return ""
	}
	return cookie.Value
}

func ownerID(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

func setOwnerCookie(w http.ResponseWriter, r *http.Request, name, token, path string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    token,
		Path:     path,
		MaxAge:   ownerCookieAge,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
}

func writeInvalidBody(w http.ResponseWriter, err error) {
	var limit *http.MaxBytesError
	if errors.As(err, &limit) {
		writeError(w, http.StatusRequestEntityTooLarge, "message body is too large")
		return
	}
	writeError(w, http.StatusBadRequest, "expected one JSON object with a text field")
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
