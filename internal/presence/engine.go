package presence

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/ider-zh/lanwatch/internal/config"
	"github.com/ider-zh/lanwatch/internal/source"
	"github.com/ider-zh/lanwatch/internal/store"
)

// EventType 事件类型常量。
const (
	TypeOnline          = "online"
	TypeOffline         = "offline"
	TypeNewDevice       = "new_device"
	TypeIPChanged       = "ip_changed"
	TypeHostnameChanged = "hostname_changed"
	TypeFlapping        = "flapping"
)

// Event 状态机产出的事件，供通知引擎 / CLI 消费。
type Event struct {
	Type      string
	MAC       string
	IP        string
	Hostname  string
	Vendor    string
	SSID      string
	LinkType  string
	SourceID  string
	At        int64
	Flapping  bool
	DurationS *int64 // offline 事件：已离线时长
	LastSeen  int64  // 上次在线时刻
}

// Engine 在线状态机：归并多源证据、维护每设备状态、产出区间与事件。
type Engine struct {
	st     *store.Store
	cfg    config.PresenceConfig
	log    *slog.Logger
	mu     sync.Mutex
	states map[string]*deviceState
	events chan Event
}

func New(st *store.Store, cfg config.PresenceConfig, log *slog.Logger) *Engine {
	if log == nil {
		log = slog.Default()
	}
	return &Engine{
		st:     st,
		cfg:    cfg,
		log:    log,
		states: map[string]*deviceState{},
		events: make(chan Event, 1024),
	}
}

// Events 返回事件通道（供上层消费）。
func (e *Engine) Events() <-chan Event { return e.events }

// Init 从 DB 已打开的在线区间恢复内存状态，避免重启后重复 emit online。
func (e *Engine) Init(ctx context.Context) error {
	rows, err := e.st.Query(`SELECT mac FROM presence_sessions WHERE end_at IS NULL`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var mac string
		if err := rows.Scan(&mac); err != nil {
			return err
		}
		e.states[mac] = &deviceState{state: StOnline, hits: e.cfg.HitThreshold}
	}
	return rows.Err()
}

func (e *Engine) emit(ev Event) {
	select {
	case e.events <- ev:
	default:
		e.log.Warn("event channel full, dropping", "type", ev.Type, "mac", ev.MAC)
	}
}

// ProcessRound 处理一轮采集结果（由 scheduler 调用）。
func (e *Engine) ProcessRound(ctx context.Context, rr *source.RoundResult) {
	e.mu.Lock()
	defer e.mu.Unlock()

	now := time.Now().Unix()

	// 1. 区分健康/不健康源；不健康源的观测（含"全部缺席"）一律不计入判定
	healthySources := map[string]bool{}
	for srcID := range rr.BySource {
		if st, err := e.st.GetSourceState(srcID); err == nil && st != nil && !st.Healthy {
			e.log.Warn("source unhealthy, ignoring its observations", "id", srcID)
			continue
		}
		healthySources[srcID] = true
	}
	if len(healthySources) == 0 {
		e.log.Warn("all sources unhealthy this round; leaving device states unchanged")
		return
	}

	merged := map[string]*mergedObs{}
	for srcID, obs := range rr.BySource {
		if !healthySources[srcID] {
			continue
		}
		for i := range obs {
			o := obs[i]
			m := source.NormalizeMAC(o.MAC)
			mo, ok := merged[m]
			if !ok {
				mo = &mergedObs{}
				merged[m] = mo
			}
			if o.Present {
				mo.present = true
			}
			if o.Confidence > mo.conf {
				mo.conf = o.Confidence
				mo.ip = o.IP
				mo.hostname = o.Hostname
				mo.vendor = o.Vendor
				mo.ssid = o.SSID
				mo.linkType = string(o.LinkType)
				mo.sourceID = o.SourceID
			} else if mo.ip == "" {
				// 兜底：至少带一个 IP
				mo.ip = o.IP
				mo.hostname = o.Hostname
				mo.sourceID = o.SourceID
			}
		}
	}

	// 2. 处理本轮观测到的设备
	for mac, mo := range merged {
		e.processDevice(mac, mo, now)
	}

	// 3. 已知设备但本轮未在健康源中出现 -> 计一次 miss（merged 为空时即全部缺席）
	for mac := range e.states {
		if _, ok := merged[mac]; ok {
			continue
		}
		e.registerMiss(mac, now)
	}
}

type mergedObs struct {
	present  bool
	conf     int
	ip       string
	hostname string
	vendor   string
	ssid     string
	linkType string
	sourceID string
}
