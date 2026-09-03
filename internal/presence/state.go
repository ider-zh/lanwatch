package presence

import (
	"github.com/ider-zh/lanwatch/internal/store"
)

// State 设备在线状态。
type State string

const (
	StUnknown State = "unknown"
	StOnline  State = "online"
	StOffline State = "offline"
)

type deviceState struct {
	state       State
	hits        int
	misses      int
	transitions []int64 // 最近 online<->offline 切换时刻
	flapping    bool
	flapUntil   int64
}

// processDevice 处理单个被观测到的设备：登记/合并设备、检测属性变化、推进状态机。
func (e *Engine) processDevice(mac string, mo *mergedObs, now int64) {
	ds, ok := e.states[mac]
	if !ok {
		ds = &deviceState{state: StUnknown}
		e.states[mac] = ds
		// 新设备：自动纳入
		dev := store.Device{
			MAC:       mac,
			Name:      "",
			Hostname:  mo.hostname,
			Vendor:    mo.vendor,
			CurrentIP: mo.ip,
			LinkType:  mo.linkType,
			SSID:      mo.ssid,
			Status:    "new",
			FirstSeen: now,
			LastSeen:  now,
		}
		if err := e.st.UpsertDevice(dev); err != nil {
			e.log.Warn("upsert new device", "mac", mac, "err", err)
		}
		e.emit(Event{
			Type: TypeNewDevice, MAC: mac, IP: mo.ip, Hostname: mo.hostname,
			Vendor: mo.vendor, SSID: mo.ssid, LinkType: mo.linkType,
			SourceID: mo.sourceID, At: now,
		})
	} else {
		// 属性变化检测（仅在观察到时更新）
		e.detectChanges(mac, mo, now)
	}

	if mo.present {
		e.registerHit(mac, now)
	} else {
		e.registerMiss(mac, now)
	}
}

// detectChanges 对比并落库属性变化，emit 对应事件。
func (e *Engine) detectChanges(mac string, mo *mergedObs, now int64) {
	dev, err := e.st.GetDevice(mac)
	if err != nil || dev == nil {
		return
	}
	changedIP := mo.ip != "" && mo.ip != dev.CurrentIP
	changedHost := mo.hostname != "" && mo.hostname != dev.Hostname
	if !changedIP && !changedHost {
		// 无属性变化：不再更新 last_seen。last_seen 表示"最近一次上线"，
		// 只在真正进入在线时更新（见 registerHit），避免每次观测都刷成"1分钟前"。
		return
	}
	upd := store.Device{MAC: mac}
	if changedIP {
		upd.CurrentIP = mo.ip
	}
	if changedHost {
		upd.Hostname = mo.hostname
	}
	if err := e.st.UpsertDevice(upd); err != nil {
		e.log.Warn("update device attr", "mac", mac, "err", err)
		return
	}
	if changedIP {
		e.emit(Event{Type: TypeIPChanged, MAC: mac, IP: mo.ip, Hostname: dev.Hostname, At: now})
	}
	if changedHost {
		e.emit(Event{Type: TypeHostnameChanged, MAC: mac, IP: mo.ip, Hostname: mo.hostname, At: now})
	}
}

// registerHit 确认在线计数 + 状态转移（含 merge-gap 合并）。
func (e *Engine) registerHit(mac string, now int64) {
	ds := e.states[mac]
	ds.hits++
	ds.misses = 0

	if ds.state == StOnline {
		return
	}
	if ds.hits < e.cfg.HitThreshold {
		return
	}

	// 是否可合并到上一个刚关闭的短区间
	merge := false
	if latest, err := e.st.LatestSession(mac); err == nil && latest != nil && latest.EndAt != nil {
		gap := now - *latest.EndAt
		if gap <= int64(e.cfg.SessionMergeGap.Value().Seconds()) {
			merge = true
		}
	}
	if merge {
		if err := e.st.ReopenLatestSession(mac); err != nil {
			e.log.Warn("reopen session", "mac", mac, "err", err)
		}
		// 视为连续在线，不 emit online，不计切换
	} else {
		if _, err := e.st.OpenSession(mac, now, "online"); err != nil {
			e.log.Warn("open session", "mac", mac, "err", err)
		}
		// last_seen 表示"最近一次上线"，仅在进入在线这一刻更新（而非每次观测都刷）。
		if err := e.st.UpsertDevice(store.Device{MAC: mac, LastSeen: now}); err != nil {
			e.log.Warn("update last_seen on online", "mac", mac, "err", err)
		}
		// 抖动(Flapping)不再在 presence 层丢弃事件：事件照常发出并带上 Flapping 标记，
		// 由通知引擎按 (设备,渠道,事件) 冷却去重决定最终是否推送（原设计意图，见 suppressed()）。
		// 否则一旦进入 flapping，30 分钟内所有离线/上线事件被静默，导致「上线通知了、离线没通知」。
		e.recordTransition(mac, now)
		e.emit(Event{Type: TypeOnline, MAC: mac, At: now, Flapping: ds.flapping})
	}
	ds.state = StOnline
}

// registerMiss 未观测到计数 + 状态转移（离线判定）。
func (e *Engine) registerMiss(mac string, now int64) {
	ds := e.states[mac]
	ds.misses++
	ds.hits = 0

	if ds.state != StOnline {
		return
	}
	if ds.misses < e.cfg.MissThreshold {
		return
	}

	if err := e.st.CloseOpenSessions(mac, now, "offline"); err != nil {
		e.log.Warn("close session", "mac", mac, "err", err)
	}
	latest, _ := e.st.LatestSession(mac)
	var dur *int64
	var lastSeen int64
	if latest != nil {
		d := now - latest.StartAt
		dur = &d
		lastSeen = latest.StartAt
	}
	// 抖动(Flapping)不再在 presence 层丢弃离线事件：照常发出并带 Flapping 标记，
	// 由通知引擎按冷却去重处理，避免「设备已稳定在线后又离线却收不到通知」。
	e.recordTransition(mac, now)
	e.emit(Event{
		Type: TypeOffline, MAC: mac, At: now,
		DurationS: dur, LastSeen: lastSeen, Flapping: ds.flapping,
	})
	ds.state = StOffline
}
