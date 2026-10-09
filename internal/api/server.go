package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ider-zh/lanwatch/internal/config"
	"github.com/ider-zh/lanwatch/internal/notify"
	"github.com/ider-zh/lanwatch/internal/presence"
	"github.com/ider-zh/lanwatch/internal/store"
)

// API 聚合 store / presence engine / notify engine / config，提供 HTTP 接口。
type API struct {
	st      *store.Store
	engine  *presence.Engine
	cfg     *config.Config
	log     *slog.Logger
	lim     *loginLimiter
	notify  *notify.Engine
	crypter *notify.Crypter
}

func New(st *store.Store, engine *presence.Engine, cfg *config.Config, notifyEngine *notify.Engine, crypter *notify.Crypter, log *slog.Logger) *API {
	if log == nil {
		log = slog.Default()
	}
	return &API{
		st:      st,
		engine:  engine,
		cfg:     cfg,
		log:     log,
		lim:     newLoginLimiter(10, 5*time.Minute),
		notify:  notifyEngine,
		crypter: crypter,
	}
}

// Handler 返回挂载好路由的 http.Handler。
func (a *API) Handler() http.Handler {
	r := chi.NewRouter()

	// 无需鉴权
	r.Get("/api/health", a.handleHealth)
	r.Post("/api/auth/login", a.handleLogin)

	// 需鉴权
	r.Group(func(protected chi.Router) {
		protected.Use(a.requireAuth)
		protected.Get("/api/auth/me", a.handleMe)
		protected.Post("/api/auth/logout", a.handleLogout)
		protected.Get("/api/devices", a.handleDevices)
		protected.Get("/api/devices/{mac}/sessions", a.handleDeviceSessions)
		protected.Get("/api/devices/{mac}/calendar", a.handleDeviceCalendar)
		protected.Patch("/api/devices/{mac}", a.handlePatchDevice)
		protected.Get("/api/timeline", a.handleTimeline)
		protected.Get("/api/sources", a.handleSources)
		protected.Get("/api/stats/overview", a.handleStatsOverview)
		protected.Get("/api/channels", a.handleListChannels)
		protected.Post("/api/channels", a.handleCreateChannel)
		protected.Put("/api/channels/{id}", a.handleUpdateChannel)
		protected.Delete("/api/channels/{id}", a.handleDeleteChannel)
		protected.Post("/api/channels/{id}/test", a.handleTestChannel)
		protected.Get("/api/rules", a.handleListRules)
		protected.Post("/api/rules", a.handleCreateRule)
		protected.Put("/api/rules/{id}", a.handleUpdateRule)
		protected.Delete("/api/rules/{id}", a.handleDeleteRule)
		protected.Get("/api/notifications", a.handleListNotifications)
		protected.Post("/api/notifications/{id}/retry", a.handleRetryNotification)
		protected.Get("/api/devices/{mac}/subscriptions", a.handleListSubscriptions)
		protected.Put("/api/devices/{mac}/subscriptions", a.handlePutSubscriptions)
	})

	// 前端 SPA（非 /api 路径回退到 index.html）
	r.Handle("/*", a.staticHandler())

	return r
}

// userKey 上下文键。
type ctxKey int

const userKey ctxKey = iota

func (a *API) userFromContext(ctx context.Context) *store.User {
	if u, ok := ctx.Value(userKey).(*store.User); ok {
		return u
	}
	return nil
}
