package notify

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ider-zh/lanwatch/internal/config"
	"github.com/ider-zh/lanwatch/internal/presence"
	"github.com/ider-zh/lanwatch/internal/store"
)

type fakeSender struct {
	id    int64
	mu    sync.Mutex
	count int
	items []TemplateData
	fail  bool
}

func (f *fakeSender) Send(_ context.Context, data TemplateData) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count++
	f.items = append(f.items, data)
	if f.fail {
		return errors.New("boom")
	}
	return nil
}

func (f *fakeSender) SendDigest(_ context.Context, items []TemplateData) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count += len(items)
	f.items = append(f.items, items...)
	return nil
}

func (f *fakeSender) ChannelID() int64 { return f.id }

func (f *fakeSender) snapshot() (int, []TemplateData) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]TemplateData, len(f.items))
	copy(out, f.items)
	return f.count, out
}

func newTestEngine(t *testing.T, cfg config.NotifyConfig) (*Engine, *store.Store, func()) {
	t.Helper()
	db := t.TempDir() + "/n.db"
	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	e := New(st, NewCrypter("secret"), cfg, "http://localhost:8080", nil)
	return e, st, func() { e.Stop(); st.Close() }
}

func offlineEvent(mac string) presence.Event {
	return presence.Event{Type: "offline", MAC: mac, IP: "192.168.1.9", Hostname: "phone",
		Vendor: "Apple", LinkType: "wifi", SSID: "HOME", SourceID: "asus-1", At: time.Now().Unix()}
}

func TestEngineCooldownDedup(t *testing.T) {
	e, st, stop := newTestEngine(t, config.NotifyConfig{CooldownSec: 300, RateLimitPerChannel: 100})
	defer stop()
	f := &fakeSender{id: 1}
	e.RegisterSender(1, "dingtalk", f)

	mac := "AA-BB-CC-DD-EE-FF"
	if err := st.SetSubscription(mac, 1, `["offline"]`); err != nil {
		t.Fatal(err)
	}

	e.Handle(offlineEvent(mac))
	if !waitFor(t, func() bool {
		n, _ := f.snapshot()
		return n >= 1
	}, 3*time.Second) {
		t.Fatal("first send did not happen")
	}
	e.Handle(offlineEvent(mac)) // 冷却期内应被去重
	if !waitFor(t, func() bool {
		n, _ := f.snapshot()
		return n == 1
	}, 3*time.Second) {
		n, _ := f.snapshot()
		t.Fatalf("expected 1 send (cooldown dedup), got %d", n)
	}
}

func TestEngineRetryExhausted(t *testing.T) {
	cfg := config.NotifyConfig{CooldownSec: 0, RateLimitPerChannel: 100, Retry: []config.Duration{
		durationMS(1), durationMS(1),
	}}
	e, st, stop := newTestEngine(t, cfg)
	defer stop()
	f := &fakeSender{id: 1, fail: true}
	e.RegisterSender(1, "dingtalk", f)

	mac := "AA-BB-CC-DD-EE-FF"
	_ = st.SetSubscription(mac, 1, `["offline"]`)

	e.Handle(offlineEvent(mac))
	if !waitFor(t, func() bool {
		n, _ := f.snapshot()
		return n == 3
	}, 5*time.Second) {
		n, _ := f.snapshot()
		t.Fatalf("expected 3 send attempts (1 + 2 retries), got %d", n)
	}
	logs, err := st.ListNotifyLogs(10, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) == 0 {
		t.Fatal("expected a failed notify log")
	}
	if logs[0].Status != "failed" {
		t.Fatalf("expected failed status, got %q", logs[0].Status)
	}
}

func TestEngineRateLimitDigest(t *testing.T) {
	cfg := config.NotifyConfig{CooldownSec: 0, RateLimitPerChannel: 1}
	e, st, stop := newTestEngine(t, cfg)
	defer stop()
	f := &fakeSender{id: 1}
	e.RegisterSender(1, "dingtalk", f)

	mac := "AA-BB-CC-DD-EE-FF"
	_ = st.SetSubscription(mac, 1, `["offline"]`)

	for i := 0; i < 5; i++ {
		e.Handle(offlineEvent(mac))
		time.Sleep(5 * time.Millisecond)
	}
	// 限流：1 条直发，4 条缓冲
	if !waitFor(t, func() bool {
		n, _ := f.snapshot()
		return n == 1
	}, 3*time.Second) {
		n, _ := f.snapshot()
		t.Fatalf("expected 1 direct send before flush, got %d", n)
	}
	// 触发摘要合并发送
	e.flushDigests()
	if !waitFor(t, func() bool {
		n, _ := f.snapshot()
		return n == 5
	}, 3*time.Second) {
		n, items := f.snapshot()
		t.Fatalf("expected 5 total (1 direct + 4 digest), got %d (items=%d)", n, len(items))
	}
	_, items := f.snapshot()
	if len(items) != 5 {
		t.Fatalf("expected 5 items, got %d", len(items))
	}
}

func durationMS(ms int) config.Duration {
	return config.Duration(time.Duration(ms) * time.Millisecond)
}

// waitFor 轮询直到 cond() 为真或超时（sqlite 写入较慢，发送为异步 goroutine）。
func waitFor(t *testing.T, cond func() bool, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
	return true
}
