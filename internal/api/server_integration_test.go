package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/ider-zh/lanwatch/internal/config"
	"github.com/ider-zh/lanwatch/internal/notify"
	"github.com/ider-zh/lanwatch/internal/store"
)

// 通过完整 Handler()（含 requireAuth 中间件）验证鉴权与受保护路由。
func TestServerAuthAndChannelFlow(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	defer os.Remove(dbPath)

	hash, _ := bcrypt.GenerateFromPassword([]byte("secret-pw"), 8)
	if _, err := st.CreateUser("admin", string(hash), "admin"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	crypter := notify.NewCrypter("test-secret")
	cfg := &config.Config{ListenAddr: "127.0.0.1:8080"}
	a := New(st, nil, cfg, nil, crypter, nil)
	handler := a.Handler()

	// 未带 cookie 访问受保护接口应 401
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/channels", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without cookie, got %d", rec.Code)
	}

	// 登录获取 cookie
	loginBody, _ := json.Marshal(map[string]string{"username": "admin", "password": "secret-pw"})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no session cookie set")
	}
	cookie := cookies[0]

	// 带 cookie 创建渠道
	chBody, _ := json.Marshal(map[string]interface{}{
		"type": "dingtalk", "name": "D", "enabled": true,
		"config": map[string]interface{}{"webhook": "https://x", "secret": "SEC", "msgtype": "markdown"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/channels", bytes.NewReader(chBody))
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create channel failed: %d %s", rec.Code, rec.Body.String())
	}

	// 带 cookie 列出渠道，应至少 1 条
	req = httptest.NewRequest(http.MethodGet, "/api/channels", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list channels failed: %d", rec.Code)
	}
	var list struct {
		Channels []map[string]interface{} `json:"channels"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Channels) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(list.Channels))
	}
}
