package asus

import (
	"testing"

	"github.com/ider-zh/lanwatch/internal/source"
)

// fakeRunner 返回固定输出，用于测试解析逻辑。
type fakeRunner struct{ out string }

func (f *fakeRunner) Run(script string) (string, error) { return f.out, nil }
func (f *fakeRunner) Close() error                      { return nil }

func newTestAdapter(out string) *Adapter {
	return NewWithRunner("asus-1", "br0", "192.168.1", 10, true,
		&fakeRunner{out: out}, nil)
}

func TestParseNormalRound(t *testing.T) {
	out := `P 192.168.1.50
###NEIGH
192.168.1.1 dev br0 lladdr AA:BB:CC:DD:EE:01 REACHABLE
192.168.1.50 dev br0 lladdr 11:22:33:44:55:66 REACHABLE
192.168.1.99 dev br0 INCOMPLETE
192.168.1.80 dev br0 lladdr 22:22:22:22:22:22 STALE
###LEASES
4100000000 22-22-22-22-22-22 192.168.1.80 phone-80 *
duid 00:01:00:01:ab:cd:ef
`
	a := newTestAdapter(out)
	obs, reconfirm := a.parse(out)

	byMac := map[string]source.Observation{}
	for _, o := range obs {
		byMac[o.MAC] = o
	}

	// INCOMPLETE (.99) 必须被过滤掉
	if _, ok := byMac["192.168.1.99"]; ok {
		t.Fatal("INCOMPLETE entry should be filtered")
	}
	// duid 行不能产生设备
	for _, o := range obs {
		if o.MAC == "DUID" || o.Hostname == "duid" {
			t.Fatal("duid line should be skipped")
		}
	}

	// .1 与 .50 应在线（MAC 已规范化为连字符形式）
	for _, mac := range []string{"AA-BB-CC-DD-EE-01", "11-22-33-44-55-66"} {
		o, ok := byMac[mac]
		if !ok {
			t.Fatalf("missing device %s", mac)
		}
		if !o.Present {
			t.Errorf("%s should be present (REACHABLE)", mac)
		}
		if o.Confidence != confReachable {
			t.Errorf("%s confidence want %d got %d", mac, confReachable, o.Confidence)
		}
	}

	// .80 STALE + lease -> 不在线，但应带 hostname
	o80, ok := byMac["22-22-22-22-22-22"]
	if !ok {
		t.Fatal("missing .80 device")
	}
	if o80.Present {
		t.Error(".80 STALE should not be present")
	}
	if o80.Hostname != "phone-80" {
		t.Errorf(".80 hostname want phone-80 got %q", o80.Hostname)
	}

	// STALE 设备应进入下一轮复核
	if !reconfirm["192.168.1.80"] {
		t.Error(".80 (STALE) should be in reconfirm set")
	}
	if reconfirm["192.168.1.50"] {
		t.Error(".50 (REACHABLE) should NOT be in reconfirm set")
	}
}

func TestParsePingConfirmsNonReachable(t *testing.T) {
	// 设备不在 neigh REACHABLE，但 ping 命中 -> 应判在线
	out := `P 192.168.1.77
###NEIGH
192.168.1.77 dev br0 lladdr 99:88:77:66:55:44 DELAY
###LEASES
`
	a := newTestAdapter(out)
	obs, _ := a.parse(out)
	if len(obs) != 1 {
		t.Fatalf("want 1 device, got %d", len(obs))
	}
	if !obs[0].Present {
		t.Error("ping-hit DELAY device should be present")
	}
	if obs[0].Confidence != confPing {
		t.Errorf("confidence want %d got %d", confPing, obs[0].Confidence)
	}
}
