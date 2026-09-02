package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookie = "lw_session"
	sessionTTL    = 12 * time.Hour
)

func hashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
	return string(h), err
}
func checkPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// newToken 生成 32 字节随机 token（raw 用于种 cookie），stored 为 raw 的 SHA256 摘要存库，
// 即使库泄露也无法反推明文 token。
func newToken() (raw, stored string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return
	}
	raw = hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(raw))
	stored = hex.EncodeToString(sum[:])
	return
}

func tokenHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (a *API) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !a.lim.allow(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	u, err := a.st.GetUserByUsername(req.Username)
	if err != nil {
		logCtx(a.log, r).Error("get user", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if u == nil || !checkPassword(u.PasswordHash, req.Password) {
		a.lim.hit(clientIP(r))
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	raw, stored, err := newToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	now := time.Now().Unix()
	expires := now + int64(sessionTTL.Seconds())
	if err := a.st.SaveSession(stored, u.ID, now, expires, now); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	_ = a.st.SetLastLogin(u.ID, now)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    raw,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"user": map[string]string{"username": u.Username, "role": u.Role},
	})
}

func (a *API) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = a.st.DeleteSession(tokenHash(c.Value))
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1,
	})
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (a *API) handleMe(w http.ResponseWriter, r *http.Request) {
	u := a.userFromContext(r.Context())
	if u == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user": map[string]string{"username": u.Username, "role": u.Role},
	})
}

func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return fwd
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// loginLimiter 固定窗口限流（每 IP）。
type loginLimiter struct {
	mu       sync.Mutex
	max      int
	window   time.Duration
	attempts map[string]int
	resetAt  map[string]time.Time
}

func newLoginLimiter(max int, window time.Duration) *loginLimiter {
	return &loginLimiter{
		max:      max,
		window:   window,
		attempts: map[string]int{},
		resetAt:  map[string]time.Time{},
	}
}

func (l *loginLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if t, ok := l.resetAt[ip]; !ok || now.After(t) {
		l.attempts[ip] = 0
		l.resetAt[ip] = now.Add(l.window)
	}
	return l.attempts[ip] < l.max
}

func (l *loginLimiter) hit(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempts[ip]++
}
