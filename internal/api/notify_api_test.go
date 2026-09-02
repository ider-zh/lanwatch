package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ider-zh/lanwatch/internal/config"
	"github.com/ider-zh/lanwatch/internal/notify"
	"github.com/ider-zh/lanwatch/internal/store"
)

func newTestAPI(t *testing.T) (*API, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	crypter := notify.NewCrypter("test-secret")
	cfg := &config.Config{ListenAddr: "127.0.0.1:8080"}
	api := New(st, nil, cfg, nil, crypter, nil)
	return api, func() { st.Close(); os.Remove(dbPath) }
}

func doJSON(a *API, method, path string, body interface{}, h func(http.ResponseWriter, *http.Request), params ...map[string]string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	if len(params) > 0 {
		rctx := chi.NewRouteContext()
		for k, v := range params[0] {
			rctx.URLParams.Add(k, v)
		}
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestChannelCRUDAndMasking(t *testing.T) {
	a, cleanup := newTestAPI(t)
	defer cleanup()

	// 创建邮件渠道（含明文密码）
	create := map[string]interface{}{
		"type":    "email",
		"name":    "告警邮箱",
		"enabled": true,
		"config": map[string]interface{}{
			"host":     "smtp.example.com",
			"port":     "465",
			"username": "alert@example.com",
			"password": "S3cretPass",
			"from":     "alert@example.com",
			"to":       []string{"me@example.com"},
			"tls":      "ssl",
			"subject":  "设备告警",
		},
	}
	rec := doJSON(a, http.MethodPost, "/api/channels", create, a.handleCreateChannel)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create channel status = %d body=%s", rec.Code, rec.Body.String())
	}
	var cresp struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &cresp)
	if cresp.ID == 0 {
		t.Fatal("expected channel id")
	}

	// 列表：密码应被脱敏为 ***
	rec = doJSON(a, http.MethodGet, "/api/channels", nil, a.handleListChannels)
	if rec.Code != http.StatusOK {
		t.Fatalf("list channels status = %d", rec.Code)
	}
	var list struct {
		Channels []map[string]interface{} `json:"channels"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Channels) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(list.Channels))
	}
	cfg := list.Channels[0]["config"].(map[string]interface{})
	if cfg["password"] != "***" {
		t.Fatalf("password not masked: %v", cfg["password"])
	}
	// 落库应为密文，而非明文
	ch, err := a.st.GetChannel(cresp.ID)
	if err != nil {
		t.Fatal(err)
	}
	var dec map[string]interface{}
	if err := a.crypter.DecryptJSON(ch.ConfigEnc, &dec); err != nil {
		t.Fatal(err)
	}
	if dec["password"] != "S3cretPass" {
		t.Fatalf("stored password wrong: %v", dec["password"])
	}

	// 更新：密码字段传 *** 表示不变，应保留原值
	update := map[string]interface{}{
		"type":    "email",
		"name":    "告警邮箱改",
		"enabled": true,
		"config": map[string]interface{}{
			"host":     "smtp.example.com",
			"port":     "465",
			"username": "alert@example.com",
			"password": "***",
			"from":     "alert@example.com",
			"to":       []string{"me@example.com"},
			"tls":      "ssl",
			"subject":  "设备告警",
		},
	}
	rec = doJSON(a, http.MethodPut, "/api/channels/1", update, a.handleUpdateChannel, map[string]string{"id": "1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("update channel status = %d body=%s", rec.Code, rec.Body.String())
	}
	ch, _ = a.st.GetChannel(cresp.ID)
	if err := a.crypter.DecryptJSON(ch.ConfigEnc, &dec); err != nil {
		t.Fatal(err)
	}
	if dec["password"] != "S3cretPass" {
		t.Fatalf("password should be preserved on *** sentinel, got %v", dec["password"])
	}
}

func TestRuleCRUDAndEventArray(t *testing.T) {
	a, cleanup := newTestAPI(t)
	defer cleanup()

	create := map[string]interface{}{
		"name":         "默认离线通知",
		"enabled":      true,
		"scope":        "all",
		"events":       []string{"online", "offline"},
		"cooldown_sec": 300,
		"channel_ids":  []int64{},
	}
	rec := doJSON(a, http.MethodPost, "/api/rules", create, a.handleCreateRule)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create rule status = %d body=%s", rec.Code, rec.Body.String())
	}
	rec = doJSON(a, http.MethodGet, "/api/rules", nil, a.handleListRules)
	if rec.Code != http.StatusOK {
		t.Fatalf("list rules status = %d", rec.Code)
	}
	var list struct {
		Rules []map[string]interface{} `json:"rules"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(list.Rules))
	}
	events, ok := list.Rules[0]["events"].([]interface{})
	if !ok || len(events) != 2 {
		t.Fatalf("events not parsed as array: %v", list.Rules[0]["events"])
	}
}

func TestSubscriptionsReplace(t *testing.T) {
	a, cleanup := newTestAPI(t)
	defer cleanup()

	// 先建两个渠道
	ch1 := doJSON(a, http.MethodPost, "/api/channels", map[string]interface{}{
		"type": "dingtalk", "name": "D1", "enabled": true,
		"config": map[string]interface{}{"webhook": "https://x", "secret": "SEC", "msgtype": "markdown"},
	}, a.handleCreateChannel)
	if ch1.Code != http.StatusCreated {
		t.Fatalf("create ch1 status = %d", ch1.Code)
	}
	ch2 := doJSON(a, http.MethodPost, "/api/channels", map[string]interface{}{
		"type": "dingtalk", "name": "D2", "enabled": true,
		"config": map[string]interface{}{"webhook": "https://y", "secret": "SEC", "msgtype": "markdown"},
	}, a.handleCreateChannel)
	if ch2.Code != http.StatusCreated {
		t.Fatalf("create ch2 status = %d", ch2.Code)
	}

	mac := "3C:0A:F3:D3:54:43"
	put := map[string]interface{}{
		"channels": []int64{1, 2},
		"events":   []string{"online", "offline"},
	}
	rec := doJSON(a, http.MethodPut, "/api/devices/"+mac+"/subscriptions", put, a.handlePutSubscriptions, map[string]string{"mac": mac})
	if rec.Code != http.StatusOK {
		t.Fatalf("put subscriptions status = %d body=%s", rec.Code, rec.Body.String())
	}
	subs, err := a.st.ListSubscriptions(mac)
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 2 {
		t.Fatalf("expected 2 subscriptions after replace, got %d", len(subs))
	}
	// 再次 PUT 只保留渠道 1，应删除渠道 2
	put = map[string]interface{}{
		"channels": []int64{1},
		"events":   []string{"online"},
	}
	rec = doJSON(a, http.MethodPut, "/api/devices/"+mac+"/subscriptions", put, a.handlePutSubscriptions, map[string]string{"mac": mac})
	if rec.Code != http.StatusOK {
		t.Fatalf("put subscriptions 2 status = %d", rec.Code)
	}
	subs, _ = a.st.ListSubscriptions(mac)
	if len(subs) != 1 {
		t.Fatalf("expected 1 subscription after second replace, got %d", len(subs))
	}
}

func TestListNotificationsEmpty(t *testing.T) {
	a, cleanup := newTestAPI(t)
	defer cleanup()
	rec := doJSON(a, http.MethodGet, "/api/notifications", nil, a.handleListNotifications)
	if rec.Code != http.StatusOK {
		t.Fatalf("list notifications status = %d", rec.Code)
	}
	var out struct {
		Logs []map[string]interface{} `json:"logs"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Logs == nil {
		t.Fatal("logs key missing")
	}
}
