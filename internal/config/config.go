package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration 支持 YAML 里写 "60s" / "10m" 这类可读时长。
type Duration time.Duration

func (d Duration) Value() time.Duration { return time.Duration(d) }
func (d Duration) String() string       { return time.Duration(d).String() }

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var raw string
	if err := n.Decode(&raw); err != nil {
		return err
	}
	v, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", raw, err)
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalYAML() (interface{}, error) { return time.Duration(d).String(), nil }

type Config struct {
	ListenAddr string `yaml:"listen_addr"`
	DBPath     string `yaml:"db_path"`
	Secret     string `yaml:"secret"`
	LogLevel   string `yaml:"log_level"`
	LogFormat  string `yaml:"log_format"`

	Collect   CollectConfig   `yaml:"collect"`
	Presence  PresenceConfig  `yaml:"presence"`
	Retention RetentionConfig `yaml:"retention"`
	Notify    NotifyConfig    `yaml:"notify"`
	Sources   []SourceConfig  `yaml:"sources"`
}

type CollectConfig struct {
	Interval           Duration `yaml:"interval"`
	DiscoverSweepEvery int      `yaml:"discover_sweep_every"`
	RoundTimeout       Duration `yaml:"round_timeout"`
	SweepTimeout       Duration `yaml:"sweep_timeout"`
	SSHKeepalive       Duration `yaml:"ssh_keepalive"`
	SSHTimeout         Duration `yaml:"ssh_timeout"`
	HTTPTimeout        Duration `yaml:"http_timeout"`
}

type PresenceConfig struct {
	MissThreshold       int        `yaml:"miss_threshold"`
	HitThreshold        int        `yaml:"hit_threshold"`
	SessionMergeGap     Duration   `yaml:"session_merge_gap"`
	MinOfflineNotifySec int        `yaml:"min_offline_notify_sec"`
	ConfirmPing         bool       `yaml:"confirm_ping"`
	Flap                FlapConfig `yaml:"flap"`
}

type FlapConfig struct {
	Window    Duration `yaml:"window"`
	Threshold int      `yaml:"threshold"`
	Suppress  Duration `yaml:"suppress"`
}

type RetentionConfig struct {
	ProbeSamplesDays int    `yaml:"probe_samples_days"`
	SessionsDays     int    `yaml:"sessions_days"`
	NotifyLogsDays   int    `yaml:"notify_logs_days"`
	PruneAt          string `yaml:"prune_at"`
}

type NotifyConfig struct {
	RateLimitPerChannel int        `yaml:"rate_limit_per_channel"`
	Retry               []Duration `yaml:"retry"`
	CooldownSec         int        `yaml:"cooldown_sec"`
}

type SourceConfig struct {
	ID      string   `yaml:"id"`
	Kind    string   `yaml:"kind"` // asus-ssh | tplink-http
	Enabled bool     `yaml:"enabled"`
	Subnets []string `yaml:"subnets"`
	Label   string   `yaml:"label"`

	// asus-ssh
	Host     string `yaml:"host"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	Bridge   string `yaml:"bridge"`
	HostKey  string `yaml:"host_key"`

	// tplink-http
	BaseURL string `yaml:"base_url"`
}

func defaults() Config {
	return Config{
		ListenAddr: "127.0.0.1:8080",
		DBPath:     "/var/lib/lanwatch/lanwatch.db",
		LogLevel:   "info",
		LogFormat:  "json",
		Collect: CollectConfig{
			Interval:           Duration(60 * time.Second),
			DiscoverSweepEvery: 10,
			RoundTimeout:       Duration(10 * time.Second),
			SweepTimeout:       Duration(15 * time.Second),
			SSHKeepalive:       Duration(30 * time.Second),
			SSHTimeout:         Duration(10 * time.Second),
			HTTPTimeout:        Duration(8 * time.Second),
		},
		Presence: PresenceConfig{
			MissThreshold:       2,
			HitThreshold:        1,
			SessionMergeGap:     Duration(120 * time.Second),
			MinOfflineNotifySec: 120,
			ConfirmPing:         true,
			Flap: FlapConfig{
				Window:    Duration(10 * time.Minute),
				Threshold: 4,
				Suppress:  Duration(30 * time.Minute),
			},
		},
		Retention: RetentionConfig{
			ProbeSamplesDays: 7,
			SessionsDays:     180,
			NotifyLogsDays:   30,
			PruneAt:          "03:10",
		},
		Notify: NotifyConfig{
			RateLimitPerChannel: 15,
			Retry:               []Duration{Duration(30 * time.Second), Duration(2 * time.Minute), Duration(10 * time.Minute)},
			CooldownSec:         300,
		},
	}
}

var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Load 读取 YAML，先展开 ${ENV} 再解析，最后补默认值并校验。
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	expanded := envPattern.ReplaceAllStringFunc(string(raw), func(m string) string {
		key := envPattern.FindStringSubmatch(m)[1]
		if v, ok := os.LookupEnv(key); ok {
			return v
		}
		return m
	})

	cfg := defaults()
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) Validate() error {
	if c.Secret == "" || envPattern.MatchString(c.Secret) {
		return fmt.Errorf("secret is required (set LANWATCH_SECRET or secret in config)")
	}
	if c.DBPath == "" {
		return fmt.Errorf("db_path is required")
	}
	seen := map[string]bool{}
	active := 0
	for i, s := range c.Sources {
		if s.ID == "" {
			return fmt.Errorf("sources[%d]: id is required", i)
		}
		if seen[s.ID] {
			return fmt.Errorf("sources[%d]: duplicated id %q", i, s.ID)
		}
		seen[s.ID] = true
		if !s.Enabled {
			continue
		}
		active++
		switch s.Kind {
		case "asus-ssh":
			if s.Host == "" || s.Username == "" || envPattern.MatchString(s.Password) {
				return fmt.Errorf("source %s: host/username/password required", s.ID)
			}
			if s.Bridge == "" {
				c.Sources[i].Bridge = "br0"
			}
		case "tplink-http":
			if s.BaseURL == "" || envPattern.MatchString(s.Password) {
				return fmt.Errorf("source %s: base_url/password required", s.ID)
			}
		default:
			return fmt.Errorf("source %s: unknown kind %q", s.ID, s.Kind)
		}
	}
	if active == 0 {
		return fmt.Errorf("no enabled sources")
	}
	if c.Collect.Interval.Value() <= 0 {
		return fmt.Errorf("collect.interval must be > 0")
	}
	return nil
}

func (c *Config) EnabledSources() []SourceConfig {
	out := make([]SourceConfig, 0, len(c.Sources))
	for _, s := range c.Sources {
		if s.Enabled {
			out = append(out, s)
		}
	}
	return out
}
