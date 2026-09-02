package asus

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ider-zh/lanwatch/internal/source"
)

const (
	confReachable = 90 // ip neigh = REACHABLE
	confPing      = 85 // 路由器侧 ping 通
	confDelay     = 60 // DELAY / PROBE
	confStale     = 40 // STALE
	confArpOnly   = 25 // 仅 /proc/net/arp（本适配器不取 arp，预留）
	confLease     = 15 // 仅 dnsmasq 租约未过期

	presentThreshold = 70 // 单源判定在线阈值（与设计一致）
)

// Adapter ASUS 路由器 SSH 采集适配器。
type Adapter struct {
	id          string
	host        string
	bridge      string
	base        string // 网段前缀，如 192.168.1
	sweepEvery  int
	confirmPing bool

	runner  SSHRunner
	healthy bool
	mu      sync.RWMutex
	log     *slog.Logger

	roundCount int
	confirmIPs map[string]bool // 下一轮常规轮需要复核的 IP
}

// New 用真实 SSH 连接构造适配器。
func New(id, host, user, pass, bridge string, subnets []string, sweepEvery int, confirmPing bool, hks HostKeyStore, keepalive, dialTO time.Duration, log *slog.Logger) (*Adapter, error) {
	runner, err := NewSSHRunner(host, user, pass, hks, keepalive, dialTO)
	if err != nil {
		return nil, err
	}
	base := deriveBase(host, subnets)
	if bridge == "" {
		bridge = "br0"
	}
	if sweepEvery <= 0 {
		sweepEvery = 10
	}
	return &Adapter{
		id:          id,
		host:        host,
		bridge:      bridge,
		base:        base,
		sweepEvery:  sweepEvery,
		confirmPing: confirmPing,
		runner:      runner,
		healthy:     true,
		log:         log,
		confirmIPs:  map[string]bool{},
	}, nil
}

// NewWithRunner 注入 runner（测试用）。
func NewWithRunner(id, bridge, base string, sweepEvery int, confirmPing bool, runner SSHRunner, log *slog.Logger) *Adapter {
	if bridge == "" {
		bridge = "br0"
	}
	if sweepEvery <= 0 {
		sweepEvery = 10
	}
	return &Adapter{
		id: id, bridge: bridge, base: base, sweepEvery: sweepEvery,
		confirmPing: confirmPing, runner: runner, healthy: true, log: log,
		confirmIPs: map[string]bool{},
	}
}

func (a *Adapter) ID() string   { return a.id }
func (a *Adapter) Kind() string { return "asus-ssh" }
func (a *Adapter) Healthy() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.healthy
}
func (a *Adapter) setHealthy(b bool) {
	a.mu.Lock()
	a.healthy = b
	a.mu.Unlock()
}
func (a *Adapter) Close() error { return a.runner.Close() }

func (a *Adapter) Collect(ctx context.Context) ([]source.Observation, error) {
	a.mu.Lock()
	sweep := a.sweepEvery > 0 && a.roundCount%a.sweepEvery == 0
	confirm := make([]string, 0, len(a.confirmIPs))
	for ip := range a.confirmIPs {
		confirm = append(confirm, ip)
	}
	a.roundCount++
	a.mu.Unlock()

	var script string
	if sweep {
		script = fmt.Sprintf(discoverScript, a.bridge)
		script = strings.Replace(script, "$1", a.base, 1)
	} else {
		script = fmt.Sprintf(normalScript, a.bridge)
		script = strings.Replace(script, "$1", strings.Join(confirm, " "), 1)
	}

	out, err := a.runner.Run(script)
	if err != nil {
		a.setHealthy(false)
		return nil, err
	}
	a.setHealthy(true)

	obs, nextConfirm := a.parse(out)
	a.mu.Lock()
	a.confirmIPs = nextConfirm
	a.mu.Unlock()
	return obs, nil
}

// devAcc 临时聚合一个 MAC 的多源证据。
type devAcc struct {
	mac      string
	ipConf   map[string]int // ip -> 最高置信度
	hostname string
	hasHost  bool
}

func (a *Adapter) parse(out string) ([]source.Observation, map[string]bool) {
	pingHit := map[string]bool{} // 路由器侧 ping 通的 IP
	neigh := map[string]struct { // ip -> {mac, conf, state}
		mac   string
		conf  int
		state string
	}{}
	leases := map[string]struct { // mac -> {ip, hostname}
		ip       string
		hostname string
	}{}
	ipToMac := map[string]string{}
	reconfirm := map[string]bool{} // 下一轮需复核的 IP

	section := ""
	now := time.Now().Unix()
	lines := strings.Split(out, "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "P ") {
			ip := strings.TrimSpace(strings.TrimPrefix(line, "P "))
			if ip != "" {
				pingHit[ip] = true
			}
			continue
		}
		switch line {
		case "###NEIGH":
			section = "neigh"
			continue
		case "###LEASES":
			section = "leases"
			continue
		}
		switch section {
		case "neigh":
			parseNeighLine(line, neigh, reconfirm)
		case "leases":
			parseLeaseLine(line, now, leases)
		}
	}

	// 建立 ip -> mac 映射（neigh 优先，租约补充）
	for ip, n := range neigh {
		if n.mac != "" {
			ipToMac[ip] = n.mac
		}
	}
	for mac, l := range leases {
		if l.ip != "" {
			if _, ok := ipToMac[l.ip]; !ok {
				ipToMac[l.ip] = mac
			}
		}
	}

	// 按 MAC 聚合
	acc := map[string]*devAcc{}
	addIP := func(mac, ip string, conf int) {
		mac = source.NormalizeMAC(mac)
		d, ok := acc[mac]
		if !ok {
			d = &devAcc{mac: mac, ipConf: map[string]int{}}
			acc[mac] = d
		}
		if c, ok := d.ipConf[ip]; !ok || conf > c {
			d.ipConf[ip] = conf
		}
	}
	for ip, n := range neigh {
		if n.mac == "" {
			continue
		}
		addIP(n.mac, ip, n.conf)
	}
	for mac, l := range leases {
		if l.ip == "" {
			continue
		}
		addIP(mac, l.ip, confLease)
		if l.hostname != "" && l.hostname != "*" {
			d := acc[source.NormalizeMAC(mac)]
			if d != nil && !d.hasHost {
				d.hostname = l.hostname
				d.hasHost = true
			}
		}
	}

	// ping 命中提升对应 MAC/IP 的置信度到 confPing（在 acc 建好之后）
	for ip := range pingHit {
		mac, ok := ipToMac[ip]
		if !ok {
			continue
		}
		d := acc[mac]
		if d == nil {
			continue
		}
		if c, ok := d.ipConf[ip]; !ok || confPing > c {
			d.ipConf[ip] = confPing
		}
	}

	// 生成观测
	var obs []source.Observation
	for mac, d := range acc {
		// 选择最佳 IP：优先 present 的（conf>=阈值），否则 conf 最高
		var bestIP string
		bestConf := -1
		for ip, c := range d.ipConf {
			present := c >= presentThreshold || pingHit[ip]
			if present && (bestConf < presentThreshold || c > bestConf) {
				bestIP, bestConf = ip, c
			} else if !isPresent(d, pingHit) && c > bestConf {
				bestIP, bestConf = ip, c
			}
		}
		if bestIP == "" {
			// 取任意 IP
			for ip := range d.ipConf {
				bestIP = ip
				break
			}
		}
		present := bestConf >= presentThreshold || pingHit[bestIP]
		obs = append(obs, source.Observation{
			MAC:        mac,
			IP:         bestIP,
			Hostname:   d.hostname,
			LinkType:   source.LinkUnknown,
			Present:    present,
			Confidence: bestConf,
			SourceID:   a.id,
		})
	}
	return obs, reconfirm
}

func isPresent(d *devAcc, pingHit map[string]bool) bool {
	for ip, c := range d.ipConf {
		if c >= presentThreshold || pingHit[ip] {
			return true
		}
	}
	return false
}

func parseNeighLine(line string, neigh map[string]struct {
	mac   string
	conf  int
	state string
}, reconfirm map[string]bool) {
	// 形如: 192.168.1.50 dev br0 lladdr AA:BB:.. REACHABLE
	//       192.168.1.99 dev br0 INCOMPLETE
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return
	}
	ip := fields[0]
	if strings.Contains(ip, ":") {
		return // IPv6，忽略
	}
	llIdx := indexOf(fields, "lladdr")
	if llIdx < 0 {
		return // 无 lladdr（INCOMPLETE/FAILED），丢弃
	}
	if llIdx+1 >= len(fields) {
		return
	}
	mac := fields[llIdx+1]
	state := fields[len(fields)-1]
	conf := confForState(state)
	neigh[ip] = struct {
		mac   string
		conf  int
		state string
	}{mac: source.NormalizeMAC(mac), conf: conf, state: state}
	// 非 REACHABLE 但有 lladdr 的，下一轮复核 ping
	if conf < confReachable && conf >= confStale {
		reconfirm[ip] = true
	}
}

func confForState(state string) int {
	switch state {
	case "REACHABLE":
		return confReachable
	case "DELAY", "PROBE":
		return confDelay
	case "STALE":
		return confStale
	default:
		return confStale
	}
}

func parseLeaseLine(line string, now int64, leases map[string]struct {
	ip       string
	hostname string
}) {
	fields := strings.Fields(line)
	if len(fields) < 4 {
		return
	}
	if fields[0] == "duid" {
		return // 跳过 duid 行
	}
	expires, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return // 非数值首字段，跳过
	}
	// 标准 dnsmasq 的 expires 是绝对 Unix 时间戳：仅当它在合理的 epoch 区间（>1e9）且已过期时才丢弃。
	// 部分固件（如本机 ASUS RT-AC54U）写入的是非标准小数值（如 360），据此判过期会丢失全部主机名，
	// 进而 ASUS 静态 IP 设备在列表里只剩 MAC、没有标题。
	if expires > 1_000_000_000 && expires < now {
		return // 租约已过期
	}
	mac := source.NormalizeMAC(fields[1])
	ip := fields[2]
	hostname := ""
	if len(fields) >= 4 {
		hostname = fields[3]
	}
	leases[mac] = struct {
		ip       string
		hostname string
	}{ip: ip, hostname: hostname}
}

func indexOf(fields []string, target string) int {
	for i, f := range fields {
		if f == target {
			return i
		}
	}
	return -1
}

// deriveBase 从 host 或 subnets 推导网段前缀（如 192.168.1）。
func deriveBase(host string, subnets []string) string {
	for _, s := range subnets {
		if idx := strings.Index(s, "/"); idx > 0 {
			s = s[:idx]
		}
		parts := strings.Split(s, ".")
		if len(parts) == 4 {
			return strings.Join(parts[:3], ".")
		}
	}
	// 退而从 host 取前三段
	h := host
	if idx := strings.LastIndex(h, ":"); idx >= 0 {
		h = h[:idx] // 去掉 :port
	}
	if idx := strings.LastIndex(h, "."); idx >= 0 {
		h = h[:idx]
		if idx2 := strings.LastIndex(h, "."); idx2 >= 0 {
			return h[:idx2]
		}
	}
	return "192.168.1"
}
