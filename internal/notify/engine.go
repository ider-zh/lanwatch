package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ider-zh/lanwatch/internal/config"
	"github.com/ider-zh/lanwatch/internal/presence"
	"github.com/ider-zh/lanwatch/internal/store"
)

// cst 中国标准时间（UTC+8），通知模板中所有时间统一按此渲染，
// 不依赖容器 locale（本镜像未设置 TZ，time.Local 默认是 UTC，会导致差 8 小时）。
var cst = time.FixedZone("CST", 8*3600)

// Engine 通知引擎：消费 presence 事件，展开为候选发送，应用冷却/限流/聚合/重试，
// 渲染模板并经渠道发送，所有发送写 notify_logs 审计。
type Engine struct {
	st      *store.Store
	crypter *Crypter
	cfg     config.NotifyConfig
	log     *slog.Logger
	baseURL string

	mu       sync.Mutex
	senders  map[int64]Sender
	types    map[int64]string
	cooldown map[string]int64 // key -> 上次发送 unix
	winMu    sync.Mutex
	windows  map[int64]*rateWindow // 每渠道限流窗口
	digests  map[digestKey][]TemplateData

	stop chan struct{}
	wg   sync.WaitGroup
}

type candidate struct {
	channelID   int64
	cooldownKey string
	cooldownSec int
	ruleID      *int64
	ruleName    string
}

type digestKey struct {
	channelID int64
	event     string
}

// New 构造引擎并加载启用中的渠道发送器。
func New(st *store.Store, crypter *Crypter, cfg config.NotifyConfig, baseURL string, log *slog.Logger) *Engine {
	if log == nil {
		log = slog.Default()
	}
	e := &Engine{
		st:       st,
		crypter:  crypter,
		cfg:      cfg,
		log:      log,
		baseURL:  baseURL,
		senders:  map[int64]Sender{},
		types:    map[int64]string{},
		cooldown: map[string]int64{},
		windows:  map[int64]*rateWindow{},
		digests:  map[digestKey][]TemplateData{},
		stop:     make(chan struct{}),
	}
	e.LoadSenders()
	e.wg.Add(1)
	go e.flushLoop()
	return e
}

// LoadSenders 重新从 DB 加载启用中的渠道（在渠道增删改后调用）。
func (e *Engine) LoadSenders() {
	chs, err := e.st.ListChannels()
	if err != nil {
		e.log.Warn("list channels", "err", err)
		return
	}
	m := map[int64]Sender{}
	t := map[int64]string{}
	for _, ch := range chs {
		if !ch.Enabled {
			continue
		}
		s, err := NewSender(ch, e.crypter)
		if err != nil {
			e.log.Warn("init channel sender", "id", ch.ID, "type", ch.Type, "err", err)
			continue
		}
		m[ch.ID] = s
		t[ch.ID] = ch.Type
	}
	e.mu.Lock()
	e.senders = m
	e.types = t
	e.mu.Unlock()
}

// RegisterSender 注入测试用的假发送器。
func (e *Engine) RegisterSender(id int64, typ string, s Sender) {
	e.mu.Lock()
	if e.senders == nil {
		e.senders = map[int64]Sender{}
	}
	if e.types == nil {
		e.types = map[int64]string{}
	}
	e.senders[id] = s
	e.types[id] = typ
	e.mu.Unlock()
}

// Run 持续消费事件直到 ctx 取消。
func (e *Engine) Run(ctx context.Context, events <-chan presence.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			e.Handle(ev)
		}
	}
}

// Stop 停止引擎（含摘要刷新循环）。
func (e *Engine) Stop() {
	close(e.stop)
	e.wg.Wait()
}

// Handle 处理单个事件：展开候选、冷却/限流判定、分发发送。
func (e *Engine) Handle(ev presence.Event) {
	if ev.Type == "" {
		return
	}
	cands := e.expand(ev)
	if len(cands) == 0 {
		return
	}
	data := e.toData(ev)
	now := time.Now().Unix()
	for _, c := range cands {
		e.mu.Lock()
		sender, ok := e.senders[c.channelID]
		if last, ok2 := e.cooldown[c.cooldownKey]; ok2 && now-last < int64(c.cooldownSec) {
			e.mu.Unlock()
			// 冷却跳过原本完全静默（无日志、无 notify_logs 行），线上无法定位丢通知原因，故补日志
			e.log.Info("notify skipped: cooldown", "mac", ev.MAC, "event", ev.Type,
				"channel", c.channelID, "rule", c.ruleName, "cooldown_sec", c.cooldownSec,
				"elapsed", now-last)
			continue
		}
		if !e.allow(c.channelID) {
			// 限流：入队，等待窗口结束合并为摘要
			dk := digestKey{c.channelID, ev.Type}
			e.digests[dk] = append(e.digests[dk], data)
			e.mu.Unlock()
			e.log.Info("notify queued: rate limited", "mac", ev.MAC, "event", ev.Type, "channel", c.channelID)
			continue
		}
		e.mu.Unlock()
		if !ok {
			e.log.Warn("notify skipped: no sender for channel", "mac", ev.MAC, "event", ev.Type, "channel", c.channelID)
			continue
		}
		go e.dispatch(c, sender, data)
	}
}

func (e *Engine) dispatch(c candidate, sender Sender, data TemplateData) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	attempts := len(e.cfg.Retry) + 1
	payload := e.renderPayload(c.channelID, data)
	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			d := e.cfg.Retry[i-1].Value()
			if d <= 0 {
				d = time.Second
			}
			time.Sleep(d)
		}
		logID, err := e.st.InsertNotifyLog(c.ruleID, c.channelID, macPtr(data.Device.MAC), data.Event.Type, "pending", payload)
		if err != nil {
			e.log.Warn("insert notify log", "err", err)
		}
		sendErr := sender.Send(ctx, data)
		if sendErr == nil {
			sentAt := time.Now().Unix()
			if logID > 0 {
				_ = e.st.UpdateNotifyLog(logID, "sent", i+1, "", &sentAt)
			}
			e.mu.Lock()
			e.cooldown[c.cooldownKey] = time.Now().Unix()
			e.mu.Unlock()
			return
		}
		lastErr = sendErr
		if logID > 0 {
			_ = e.st.UpdateNotifyLog(logID, "failed", i+1, sendErr.Error(), nil)
		}
	}
	e.log.Warn("notify failed after retries", "channel", c.channelID, "event", data.Event.Type, "err", lastErr)
}

func (e *Engine) toData(ev presence.Event) TemplateData {
	// 事件只带 MAC 与采集到的主机名；用户在「设备」页设置的备注名在 devices 表里，
	// 不查库的话模板里的 {{.Device.NameOrHost}} 永远拿不到备注名，只能回落到 MAC。
	hostname := ev.Hostname
	var name string
	if dev, err := e.st.GetDevice(ev.MAC); err == nil && dev != nil {
		name = dev.Name
		if hostname == "" {
			hostname = dev.Hostname
		}
	}
	d := TemplateData{
		Device: DeviceView{
			MAC: ev.MAC, IP: ev.IP, Hostname: hostname, Name: name, Vendor: ev.Vendor,
			LinkType: ev.LinkType, SSID: ev.SSID,
		},
		Event:      EventView{Type: ev.Type, At: time.Unix(ev.At, 0).In(cst)},
		SourceName: ev.SourceID,
		BaseURL:    e.baseURL,
		Flapping:   ev.Flapping,
	}
	if ev.DurationS != nil {
		d.Duration = (time.Duration(*ev.DurationS) * time.Second).String()
	}
	if ev.LastSeen != 0 {
		d.LastSeen = time.Unix(ev.LastSeen, 0).In(cst).Format("15:04:05")
	}
	return d
}

func (e *Engine) expand(ev presence.Event) []candidate {
	var cands []candidate
	subs, _ := e.st.ListSubscriptions(ev.MAC)
	for _, sub := range subs {
		if !contains(parseEvents(sub.Events), ev.Type) {
			continue
		}
		cands = append(cands, candidate{
			channelID:   sub.ChannelID,
			cooldownSec: e.cfg.CooldownSec,
			ruleName:    "设备订阅",
		})
	}
	rules, _ := e.st.ListRules()
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		if !contains(parseEvents(r.Events), ev.Type) {
			continue
		}
		if !scopeMatch(r, ev.MAC, ev.Type) {
			continue
		}
		chIDs, _ := e.st.ListRuleChannelIDs(r.ID)
		for _, cid := range chIDs {
			cd := r.CooldownSec
			if cd <= 0 {
				cd = e.cfg.CooldownSec
			}
			rid := r.ID
			cands = append(cands, candidate{
				channelID:   cid,
				cooldownSec: cd,
				ruleID:      &rid,
				ruleName:    r.Name,
			})
		}
	}
	unique := make([]candidate, 0, len(cands))
	byChannel := make(map[int64]int, len(cands))
	for _, cand := range cands {
		if index, exists := byChannel[cand.channelID]; exists {
			unique[index].cooldownSec = max(unique[index].cooldownSec, cand.cooldownSec)
			continue
		}
		cand.cooldownKey = fmt.Sprintf("channel:%d:%s:%s", cand.channelID, ev.MAC, ev.Type)
		byChannel[cand.channelID] = len(unique)
		unique = append(unique, cand)
	}
	return unique
}

func scopeMatch(r store.NotifyRule, mac, eventType string) bool {
	switch r.Scope {
	case "all", "":
		return true
	case "new_devices":
		return eventType == "new_device"
	case "devices":
		var macs []string
		_ = json.Unmarshal([]byte(r.ScopeRef), &macs)
		for _, m := range macs {
			if m == mac {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// flushLoop 周期性把限流缓冲合并发送。
func (e *Engine) flushLoop() {
	defer e.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-e.stop:
			return
		case <-ticker.C:
			e.flushDigests()
		}
	}
}

func (e *Engine) flushDigests() {
	e.mu.Lock()
	buffers := e.digests
	e.digests = map[digestKey][]TemplateData{}
	e.mu.Unlock()
	for key, items := range buffers {
		if len(items) == 0 {
			continue
		}
		e.mu.Lock()
		sender, ok := e.senders[key.channelID]
		e.mu.Unlock()
		if !ok {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		payload := fmt.Sprintf("[digest] %d events", len(items))
		logID, _ := e.st.InsertNotifyLog(nil, key.channelID, nil, key.event, "pending", payload)
		err := sender.SendDigest(ctx, items)
		if err != nil {
			if logID > 0 {
				_ = e.st.UpdateNotifyLog(logID, "failed", 1, err.Error(), nil)
			}
			e.log.Warn("digest send failed", "channel", key.channelID, "err", err)
		} else {
			sentAt := time.Now().Unix()
			if logID > 0 {
				_ = e.st.UpdateNotifyLog(logID, "sent", 1, "", &sentAt)
			}
		}
		cancel()
	}
}

func (e *Engine) allow(channelID int64) bool {
	e.winMu.Lock()
	w, ok := e.windows[channelID]
	if !ok {
		w = &rateWindow{}
		e.windows[channelID] = w
	}
	e.winMu.Unlock()
	limit := e.cfg.RateLimitPerChannel
	if limit <= 0 {
		limit = 15
	}
	return w.allow(time.Now().Unix(), limit, 60)
}

func (e *Engine) renderPayload(channelID int64, data TemplateData) string {
	e.mu.Lock()
	t := e.types[channelID]
	e.mu.Unlock()
	switch t {
	case "dingtalk":
		if _, text, err := renderDingtalk(data); err == nil {
			return text
		}
	case "email":
		if html, err := renderEmailHTML(data); err == nil {
			return html
		}
	}
	b, _ := json.Marshal(data)
	return string(b)
}

// ---- helpers ----

func parseEvents(s string) []string {
	var out []string
	if s == "" {
		return out
	}
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func macPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// rateWindow 滑动窗口限流计数器。
type rateWindow struct {
	mu   sync.Mutex
	hits []int64
}

func (r *rateWindow) allow(now int64, limit int, windowSec int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	cut := now - windowSec
	i := 0
	for ; i < len(r.hits); i++ {
		if r.hits[i] >= cut {
			break
		}
	}
	r.hits = r.hits[i:]
	if len(r.hits) >= limit {
		return false
	}
	r.hits = append(r.hits, now)
	return true
}
