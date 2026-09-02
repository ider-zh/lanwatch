package store

import "time"

func nowUnix() int64 { return time.Now().Unix() }

type Device struct {
	MAC             string `json:"mac"`
	Name            string `json:"name"`
	Hostname        string `json:"hostname"`
	Vendor          string `json:"vendor"`
	CurrentIP       string `json:"current_ip"`
	LinkType        string `json:"link_type"`
	SSID            string `json:"ssid"`
	Status          string `json:"status"` // new | tracked | ignored
	LikelyRandomMAC bool   `json:"likely_random_mac"`
	FirstSeen       int64  `json:"first_seen"`
	LastSeen        int64  `json:"last_seen"`
	Notes           string `json:"notes"`
}

type PresenceSession struct {
	ID        int64
	MAC       string
	StartAt   int64
	EndAt     *int64
	DurationS *int64
	EndReason string
}

type ProbeRound struct {
	ID         int64
	StartedAt  int64
	FinishedAt *int64
	OK         bool
	Skipped    bool
	Error      string
}

type ProbeSample struct {
	ID         int64
	RoundID    int64
	MAC        string
	IP         string
	Present    bool
	Confidence int
	SourceID   string
}

type SourceStateRow struct {
	ID              string
	Kind            string
	Healthy         bool
	LastOKAt        int64
	ConsecutiveFail int
	LastError       string
	LockedUntil     int64
	HostKey         string
	Label           string
}

type Channel struct {
	ID        int64
	Type      string
	Name      string
	ConfigEnc []byte
	Enabled   bool
	CreatedAt int64
}

type NotifyRule struct {
	ID            int64
	Name          string
	Enabled       bool
	Scope         string // all | devices | tags | new_devices
	ScopeRef      string // JSON
	Events        string // JSON array
	MinOfflineSec int
	QuietHours    string // JSON
	CooldownSec   int
	DigestSec     int
	CreatedAt     int64
}

type NotifyLog struct {
	ID        int64
	RuleID    *int64
	ChannelID int64
	MAC       *string
	Event     string
	Status    string // pending | sent | failed
	Attempts  int
	Error     string
	Payload   string
	CreatedAt int64
	SentAt    *int64
}

// Subscription 设备↔渠道订阅（events 为 JSON 数组字符串）。
type Subscription struct {
	MAC       string
	ChannelID int64
	Events    string
}

type User struct {
	ID           int64
	Username     string
	PasswordHash string
	Role         string
	CreatedAt    int64
	LastLoginAt  *int64
}

type SessionRow struct {
	Token      string
	UserID     int64
	CreatedAt  int64
	ExpiresAt  int64
	LastSeenAt int64
}
