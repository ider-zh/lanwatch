package presence

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ider-zh/lanwatch/internal/config"
	"github.com/ider-zh/lanwatch/internal/source"
	"github.com/ider-zh/lanwatch/internal/store"
)

func newTestEngine(t *testing.T, flapThreshold int) (*Engine, func()) {
	t.Helper()
	db := filepath.Join(t.TempDir(), "t.db")
	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	cfg := config.PresenceConfig{
		MissThreshold:       2,
		HitThreshold:        1,
		SessionMergeGap:     config.Duration(120 * time.Second),
		MinOfflineNotifySec: 0,
		Flap: config.FlapConfig{
			Window:    config.Duration(10 * time.Minute),
			Threshold: flapThreshold,
			Suppress:  config.Duration(30 * time.Minute),
		},
	}
	e := New(st, cfg, nil)
	if err := st.UpsertSourceState("asus-1", "asus-ssh", "asus-1"); err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	return e, func() { st.Close() }
}

// round 构造一个来自 asus-1 的采集轮次，macs 为 本轮回观测到的设备 -> 是否在线。
func round(macs map[string]bool) *source.RoundResult {
	rr := source.NewRoundResult()
	rr.BySource["asus-1"] = nil // 保证键存在（模拟 scheduler 行为）
	for mac, present := range macs {
		rr.BySource["asus-1"] = append(rr.BySource["asus-1"], source.Observation{
			MAC: mac, IP: "192.168.1.10", Present: present, Confidence: 90, SourceID: "asus-1",
		})
	}
	return rr
}

func collect(ch <-chan Event) []Event {
	var out []Event
	for {
		select {
		case ev := <-ch:
			out = append(out, ev)
		default:
			return out
		}
	}
}

func TestOnlineThenOffline(t *testing.T) {
	e, closeFn := newTestEngine(t, 99)
	defer closeFn()
	ctx := context.Background()

	e.ProcessRound(ctx, round(map[string]bool{"AA:AA:AA:AA:AA:AA": true}))
	evs := collect(e.Events())
	if !hasType(evs, TypeOnline) {
		t.Fatal("expected online event")
	}
	online, _ := e.st.IsOnline("AA-AA-AA-AA-AA-AA")
	if !online {
		t.Fatal("device should be online")
	}

	// 连续两轮缺席
	e.ProcessRound(ctx, round(map[string]bool{}))
	e.ProcessRound(ctx, round(map[string]bool{}))
	evs = collect(e.Events())
	if !hasType(evs, TypeOffline) {
		t.Fatal("expected offline event after 2 misses")
	}
	online, _ = e.st.IsOnline("AA-AA-AA-AA-AA-AA")
	if online {
		t.Fatal("device should be offline")
	}
}

func TestMergeGap(t *testing.T) {
	e, closeFn := newTestEngine(t, 99)
	defer closeFn()
	ctx := context.Background()

	e.ProcessRound(ctx, round(map[string]bool{"BB:BB:BB:BB:BB:BB": true})) // online
	e.ProcessRound(ctx, round(map[string]bool{}))                          // miss1
	e.ProcessRound(ctx, round(map[string]bool{}))                          // miss2 -> offline
	e.ProcessRound(ctx, round(map[string]bool{"BB:BB:BB:BB:BB:BB": true})) // 立即回来(<merge_gap) -> 合并

	online, _ := e.st.IsOnline("BB-BB-BB-BB-BB-BB")
	if !online {
		t.Fatal("device should be online after merge")
	}
	// 仅首个 online 事件，合并不应再产生 online 事件
	evs := collect(e.Events())
	onlineCount := 0
	for _, ev := range evs {
		if ev.Type == TypeOnline {
			onlineCount++
		}
	}
	if onlineCount != 1 {
		t.Fatalf("expected exactly 1 online event (merge), got %d", onlineCount)
	}
}

func TestFlapSuppress(t *testing.T) {
	// 阈值=2：两次切换即进入 flapping，并持续给事件打 Flapping 标记。
	// 注意：presence 层不再因 flapping 丢弃 online/offline 事件（交由通知引擎按冷却去重），
	// 因此两次离线都应照常发出，且带 Flapping 标记（这是修复「上线通知了、离线没通知」的关键）。
	e, closeFn := newTestEngine(t, 2)
	defer closeFn()
	ctx := context.Background()

	e.ProcessRound(ctx, round(map[string]bool{"CC:CC:CC:CC:CC:CC": true})) // online
	e.ProcessRound(ctx, round(map[string]bool{}))
	e.ProcessRound(ctx, round(map[string]bool{}))                          // offline (trans1)
	e.ProcessRound(ctx, round(map[string]bool{"CC:CC:CC:CC:CC:CC": true})) // online (trans2 -> flapping)
	e.ProcessRound(ctx, round(map[string]bool{}))
	e.ProcessRound(ctx, round(map[string]bool{})) // offline again

	evs := collect(e.Events())
	if !hasType(evs, TypeFlapping) {
		t.Fatal("expected flapping event")
	}
	offlineCount := 0
	offlineFlapping := 0
	for _, ev := range evs {
		if ev.Type == TypeOffline {
			offlineCount++
			if ev.Flapping {
				offlineFlapping++
			}
		}
	}
	if offlineCount != 2 {
		t.Fatalf("expected 2 offline events (flap no longer drops them), got %d", offlineCount)
	}
	if offlineFlapping == 0 {
		t.Fatal("expected the offline during flapping to carry Flapping=true")
	}
}

// TestOfflineEmittedDespiteFlapping 回归用户报告：设备上线后通知了，但之后再离线却没通知。
// 根因是 presence 层在 flapping 期间把 online/offline 事件整段丢弃，导致一次真实的离线被静默。
// 修复后事件照常发出（带 Flapping 标记），由通知引擎按冷却去重，离线也能收到。
func TestOfflineEmittedDespiteFlapping(t *testing.T) {
	e, closeFn := newTestEngine(t, 2)
	defer closeFn()
	ctx := context.Background()
	mac := "AB:CD:EF:01:02:03"

	e.ProcessRound(ctx, round(map[string]bool{mac: true})) // online (trans1)
	e.ProcessRound(ctx, round(map[string]bool{}))
	e.ProcessRound(ctx, round(map[string]bool{})) // offline (trans2 -> flapping), offline#1
	e.ProcessRound(ctx, round(map[string]bool{mac: true})) // 再次上线（flapping 中），online
	e.ProcessRound(ctx, round(map[string]bool{}))
	e.ProcessRound(ctx, round(map[string]bool{})) // 再次离线（flapping 中）—— 修复前被丢弃

	evs := collect(e.Events())
	offline := 0
	for _, ev := range evs {
		if ev.Type == TypeOffline {
			offline++
		}
	}
	if offline != 2 {
		t.Fatalf("expected 2 offline events (the 2nd must not be silenced by flapping), got %d", offline)
	}
}

// multiRound 构造来自多个源的采集轮次：srcMacs[sourceID] = mac -> present。
func multiRound(srcMacs map[string]map[string]bool) *source.RoundResult {
	rr := source.NewRoundResult()
	for srcID, macs := range srcMacs {
		rr.BySource[srcID] = nil // 保证键存在（模拟 scheduler 行为）
		for mac, present := range macs {
			rr.BySource[srcID] = append(rr.BySource[srcID], source.Observation{
				MAC: mac, IP: "192.168.1.10", Present: present, Confidence: 90, SourceID: srcID,
			})
		}
	}
	return rr
}

// TestMultiSourceDedup 同一 MAC 被两源观测只产生一个会话/一次 online 事件（DESIGN §16.2 #11）。
func TestMultiSourceDedup(t *testing.T) {
	e, closeFn := newTestEngine(t, 99)
	defer closeFn()
	if err := e.st.UpsertSourceState("tplink-1", "tplink-http", "tplink-1"); err != nil {
		t.Fatalf("upsert tplink state: %v", err)
	}
	ctx := context.Background()

	mac := "DD:DD:DD:DD:DD:DD"
	// 两个源都观测到同一 MAC 在线
	e.ProcessRound(ctx, multiRound(map[string]map[string]bool{
		"asus-1":   {mac: true},
		"tplink-1": {mac: true},
	}))

	evs := collect(e.Events())
	online := 0
	for _, ev := range evs {
		if ev.Type == TypeOnline {
			online++
		}
	}
	if online != 1 {
		t.Fatalf("expected exactly 1 online event for shared MAC, got %d", online)
	}
	// 数据库里该 MAC 只应有 1 条未关闭的在线会话
	open, err := e.st.CountOpenSessions(source.NormalizeMAC(mac))
	if err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if open != 1 {
		t.Fatalf("expected 1 open session, got %d", open)
	}

	// 合并语义：ASUS 在线 + TP-Link 仅 sys_arp(present=false) -> 仍应判定在线（OR）
	mac2 := "EE:EE:EE:EE:EE:EE"
	e.ProcessRound(ctx, multiRound(map[string]map[string]bool{
		"asus-1":   {mac2: true},
		"tplink-1": {mac2: false},
	}))
	if online2 := hasType(collect(e.Events()), TypeOnline); !online2 {
		t.Fatal("expected online via OR-merge of asus(present) + tplink(absent)")
	}
}

func hasType(evs []Event, typ string) bool {
	for _, e := range evs {
		if e.Type == typ {
			return true
		}
	}
	return false
}

// TestMergeKeepsLowerConfidenceHostname 回归：同一 MAC 的高置信度邻居表观测（无主机名）
// 不应覆盖低置信度 DHCP 租约观测（有主机名），否则 ASUS 等静态 IP 设备只剩 MAC、没有标题。
// 修复前 mo.hostname 只取最高置信度观测，租约主机名被丢弃。
func TestMergeKeepsLowerConfidenceHostname(t *testing.T) {
	e, closeFn := newTestEngine(t, 99)
	defer closeFn()
	ctx := context.Background()

	mac := "FF:FF:FF:FF:FF:FF"
	rr := source.NewRoundResult()
	rr.BySource["asus-1"] = []source.Observation{
		// 高置信度邻居表条目：在线、无主机名
		{MAC: mac, IP: "192.168.1.50", Present: true, Confidence: 100, SourceID: "asus-1"},
		// 低置信度 DHCP 租约：带主机名（静态 IP 设备的标题来源）
		{MAC: mac, IP: "192.168.1.50", Present: false, Confidence: 10, Hostname: "Redmi-K50-Ultra", SourceID: "asus-1"},
	}

	e.ProcessRound(ctx, rr)

	dev, err := e.st.GetDevice(source.NormalizeMAC(mac))
	if err != nil {
		t.Fatalf("get device: %v", err)
	}
	if dev.Hostname != "Redmi-K50-Ultra" {
		t.Fatalf("expected hostname from lower-confidence lease, got %q", dev.Hostname)
	}
}
