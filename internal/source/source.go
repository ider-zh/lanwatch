package source

import (
	"context"
	"strings"
	"time"
)

// LinkType 链路类型。
type LinkType string

const (
	LinkWired     LinkType = "wired"
	LinkWifi2G    LinkType = "wifi2g"
	LinkWifi5G    LinkType = "wifi5g"
	LinkWifi      LinkType = "wifi"
	LinkWifiGuest LinkType = "wifi-guest"
	LinkGuest     LinkType = "guest"
	LinkUnknown   LinkType = "unknown"
)

func (l LinkType) String() string { return string(l) }

// Observation 单源单轮对某设备的在线观测。
type Observation struct {
	MAC        string // 统一大写、连字符分隔
	IP         string
	Hostname   string
	Vendor     string
	LinkType   LinkType
	SSID       string
	Present    bool // 该源本轮是否认为在线（adapter 已按置信度阈值判定）
	Confidence int  // 0-100，证据强度
	SourceID   string
}

// Source 采集适配器接口（可插拔：asus-ssh / tplink-http / 测试桩）。
type Source interface {
	ID() string
	Kind() string
	// Collect 执行一轮采集，返回本轮所有观测。实现需自行遵守上下文超时。
	Collect(ctx context.Context) ([]Observation, error)
	// Healthy 返回最近一次采集是否健康（用于 presence 决定是否降级为 unknown）。
	Healthy() bool
	Close() error
}

// NormalizeMAC 把各种分隔符统一为大写连字符形式。
func NormalizeMAC(m string) string {
	m = strings.ToUpper(strings.TrimSpace(m))
	r := strings.NewReplacer(":", "-", ".", "-", " ", "")
	m = r.Replace(m)
	return m
}

// Event 由 presence engine 产生，供上层（通知引擎、CLI 打印）消费。
// 这里只定义类型常量，避免与 presence 包循环依赖。
type Phase string

const (
	PhaseCollectStart Phase = "collect_start"
	PhaseCollectDone  Phase = "collect_done"
)

// RoundResult 一轮采集结果汇总，scheduler 产出后交给 presence。
type RoundResult struct {
	StartedAt  int64
	FinishedAt int64
	Skipped    bool
	SourceErrs map[string]error
	// Observations 按 SourceID 分组，便于 presence 做多源归并。
	BySource map[string][]Observation
}

// NewRoundResult 构造空结果。
func NewRoundResult() *RoundResult {
	return &RoundResult{
		StartedAt:  time.Now().Unix(),
		SourceErrs: map[string]error{},
		BySource:   map[string][]Observation{},
	}
}
