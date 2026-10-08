package tplink

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ider-zh/lanwatch/internal/source"
)

// Adapter TP-Link 路由器 HTTP 采集适配器（host_info / sys_arp / network）。
//
// 风控（DESIGN §2.3 / §4.3 / §6.4）：
//   - 登录拿 stok 后复用；-40401 退避后重登一次；
//   - -40404(ESYSLOCKED) 立即锁死 20 分钟，停止一切登录尝试；
//   - 连续登录失败退避 60s -> 120s -> 300s；
//   - 连续失败 >=7 次禁止自动重登，避免把路由器打到锁定（已消耗 5/10 额度）。
type Adapter struct {
	id              string
	baseURL         string
	password        string
	passwordEncoded bool
	httpClient      *http.Client
	log             *slog.Logger

	mu           sync.Mutex
	stok         string
	healthy      bool
	loginFails   int
	backoffUntil time.Time
	lockedUntil  time.Time // 非零点表示当前处于锁定窗口
}

// New 构造 HTTP 适配器。httpTimeout<=0 时默认 8s。
func New(id, baseURL, password string, passwordEncoded bool, httpTimeout time.Duration, log *slog.Logger) *Adapter {
	if httpTimeout <= 0 {
		httpTimeout = 8 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	return &Adapter{
		id:              id,
		baseURL:         strings.TrimRight(baseURL, "/"),
		password:        password,
		passwordEncoded: passwordEncoded,
		httpClient:      &http.Client{Timeout: httpTimeout},
		healthy:         true,
		log:             log,
	}
}

func (a *Adapter) ID() string   { return a.id }
func (a *Adapter) Kind() string { return "tplink-http" }

func (a *Adapter) Healthy() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.healthy
}

func (a *Adapter) setHealthy(b bool) {
	a.mu.Lock()
	a.healthy = b
	a.mu.Unlock()
}

func (a *Adapter) Close() error { return nil }

// ensureStok 返回可用 stok；必要时（且未被限流/锁定时）登录一次。
// 内部处理锁定、退避、失败计数。
func (a *Adapter) ensureStok(ctx context.Context) (string, error) {
	a.mu.Lock()
	if a.stok != "" {
		s := a.stok
		a.mu.Unlock()
		return s, nil
	}
	if time.Now().Before(a.lockedUntil) {
		a.mu.Unlock()
		return "", fmt.Errorf("tplink %s: locked until %s", a.id, a.lockedUntil.Format(time.RFC3339))
	}
	if a.loginFails >= 7 {
		a.mu.Unlock()
		return "", fmt.Errorf("tplink %s: %d consecutive login failures; manual intervention required", a.id, a.loginFails)
	}
	if time.Now().Before(a.backoffUntil) {
		a.mu.Unlock()
		return "", fmt.Errorf("tplink %s: login backoff until %s", a.id, a.backoffUntil.Format(time.RFC3339))
	}
	a.mu.Unlock()

	stok, err := a.loginOnce(ctx)
	if err != nil {
		a.mu.Lock()
		if errors.Is(err, errSysLocked) {
			a.lockedUntil = time.Now().Add(loginLockDuration)
			a.log.Error("tplink source locked (ESYSLOCKED); stopping login attempts",
				"id", a.id, "unlock_at", a.lockedUntil.Format(time.RFC3339))
		} else {
			a.loginFails++
			a.backoffUntil = time.Now().Add(backoffFor(a.loginFails))
			a.log.Warn("tplink login failed; backing off",
				"id", a.id, "fails", a.loginFails, "backoff_until", a.backoffUntil.Format(time.RFC3339), "err", err)
		}
		a.healthy = false
		a.mu.Unlock()
		return "", err
	}

	a.mu.Lock()
	a.stok = stok
	a.loginFails = 0
	a.backoffUntil = time.Time{}
	a.lockedUntil = time.Time{}
	a.healthy = true
	a.mu.Unlock()
	return stok, nil
}

// Collect 执行一轮采集，返回本轮所有观测。
func (a *Adapter) Collect(ctx context.Context) ([]source.Observation, error) {
	stok, err := a.ensureStok(ctx)
	if err != nil {
		a.setHealthy(false)
		return nil, err
	}
	raw, err := a.ds(ctx, stok)
	if errors.Is(err, errUnauth) {
		// stok 失效：清掉后重登一次再试
		a.mu.Lock()
		a.stok = ""
		a.mu.Unlock()
		stok, err = a.ensureStok(ctx)
		if err != nil {
			a.setHealthy(false)
			return nil, err
		}
		raw, err = a.ds(ctx, stok)
	}
	if err != nil {
		a.setHealthy(false)
		return nil, err
	}
	obs, err := a.parse(raw)
	if err != nil {
		a.setHealthy(false)
		return nil, err
	}
	a.setHealthy(true)
	return obs, nil
}
