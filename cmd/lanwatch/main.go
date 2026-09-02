package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/ider-zh/lanwatch/internal/api"
	"github.com/ider-zh/lanwatch/internal/config"
	"github.com/ider-zh/lanwatch/internal/notify"
	"github.com/ider-zh/lanwatch/internal/presence"
	"github.com/ider-zh/lanwatch/internal/source"
	"github.com/ider-zh/lanwatch/internal/source/asus"
	"github.com/ider-zh/lanwatch/internal/source/tplink"
	"github.com/ider-zh/lanwatch/internal/store"
)

// storeHostKeys 用 DB 持久化 SSH 主机密钥指纹，实现 asus.HostKeyStore。
type storeHostKeys struct {
	st *store.Store
	id string
}

func (h *storeHostKeys) Known(hostport string) (string, bool) {
	m, err := h.st.GetSourceHostKeys(h.id)
	if err != nil || m == nil {
		return "", false
	}
	fp, ok := m[hostport]
	return fp, ok
}

func (h *storeHostKeys) Record(hostport, fp string) error {
	return h.st.SetSourceHostKey(h.id, hostport, fp)
}

func main() {
	configPath := flag.String("config", "/etc/lanwatch/config.yaml", "path to config.yaml")
	flag.Parse()

	logger := newLogger()

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("load config failed", "err", err)
		os.Exit(1)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		logger.Error("open store failed", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	if err := ensureAdmin(st, cfg, logger); err != nil {
		logger.Error("ensure admin user", "err", err)
		os.Exit(1)
	}

	// 构建采集源
	var sources []source.Source
	for _, sc := range cfg.EnabledSources() {
		switch sc.Kind {
		case "asus-ssh":
			keepalive := cfg.Collect.SSHKeepalive.Value()
			if keepalive <= 0 {
				keepalive = 30 * time.Second
			}
			adapter, err := asus.New(sc.ID, sc.Host, sc.Username, sc.Password, sc.Bridge,
				sc.Subnets, cfg.Collect.DiscoverSweepEvery, cfg.Presence.ConfirmPing,
				&storeHostKeys{st: st, id: sc.ID}, keepalive, cfg.Collect.SSHTimeout.Value(), logger)
			if err != nil {
				logger.Error("init asus source", "id", sc.ID, "err", err)
				os.Exit(1)
			}
			sources = append(sources, adapter)
		case "tplink-http":
			adapter := tplink.New(sc.ID, sc.BaseURL, sc.Password, cfg.Collect.HTTPTimeout.Value(), logger)
			sources = append(sources, adapter)
		default:
			logger.Warn("unknown source kind; skipping", "id", sc.ID, "kind", sc.Kind)
		}
	}
	if len(sources) == 0 {
		logger.Error("no usable sources configured")
		os.Exit(1)
	}

	engine := presence.New(st, cfg.Presence, logger)
	if err := engine.Init(context.Background()); err != nil {
		logger.Error("presence init", "err", err)
		os.Exit(1)
	}

	sched := source.NewScheduler(&cfg.Collect, st, sources, engine, logger)

	// 通知引擎：凭据加密 + 事件消费
	crypter := notify.NewCrypter(cfg.Secret)
	notifyEngine := notify.New(st, crypter, cfg.Notify, "http://"+cfg.ListenAddr, logger)

	apiSrv := api.New(st, engine, cfg, notifyEngine, crypter, logger)

	// 消费事件：既驱动通知引擎，也打印日志
	go func() {
		for ev := range engine.Events() {
			logger.Info("event",
				"type", ev.Type, "mac", ev.MAC, "ip", ev.IP,
				"hostname", ev.Hostname, "flapping", ev.Flapping)
			notifyEngine.Handle(ev)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           apiSrv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		logger.Info("http server listening", "addr", cfg.ListenAddr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http server", "err", err)
		}
	}()

	sched.Start()
	logger.Info("lanwatch started", "sources", len(sources), "interval", cfg.Collect.Interval.Value().String())

	<-ctx.Done()
	logger.Info("shutting down...")

	sched.Stop()
	notifyEngine.Stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	logger.Info("bye")
}

func newLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// ensureAdmin 在 users 为空时建立初始管理员：优先用 LANWATCH_ADMIN_PASSWORD，否则随机并写入文件。
func ensureAdmin(st *store.Store, cfg *config.Config, log *slog.Logger) error {
	n, err := st.CountUsers()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	pw := os.Getenv("LANWATCH_ADMIN_PASSWORD")
	if pw == "" {
		pw = randPassword(16)
		dir := filepath.Dir(cfg.DBPath)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		fp := filepath.Join(dir, "initial_admin.txt")
		if err := os.WriteFile(fp, []byte("username: admin\npassword: "+pw+"\n"), 0o600); err != nil {
			return err
		}
		log.Warn("created initial admin user; credential written to file", "path", fp)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
	if err != nil {
		return err
	}
	if _, err := st.CreateUser("admin", string(hash), "admin"); err != nil {
		return err
	}
	log.Info("initial admin user created", "username", "admin")
	return nil
}

func randPassword(n int) string {
	const chars = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("admin%d", time.Now().UnixNano())
	}
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b)
}
