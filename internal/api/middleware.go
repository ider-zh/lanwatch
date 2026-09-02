package api

import (
	"context"
	"net/http"
	"time"
)

// requireAuth 校验会话 cookie，并将用户注入上下文。
func (a *API) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		u, err := a.st.GetSessionUser(tokenHash(c.Value))
		if err != nil {
			logCtx(a.log, r).Error("get session user", "err", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if u == nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		_ = a.st.TouchSession(tokenHash(c.Value), time.Now().Unix())
		ctx := context.WithValue(r.Context(), userKey, u)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
