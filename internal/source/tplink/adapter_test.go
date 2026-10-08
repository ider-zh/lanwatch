package tplink

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ider-zh/lanwatch/internal/source"
)

// rawResponse 为 192.168.1.3:8888 实测响应（见 /tmp/lwtest/tplink_raw.json）。
// 含 URL 编码 hostname 场景、host_info 权威列表、sys_arp 陈旧表、LAN 前缀 192.168.99。
const rawResponse = `{"hosts_info": {"device": [], "host_info": [{"host_info_1": {"mac": "B6-95-34-C0-8D-4A", "up_speed": "0", "ssid": "LMD326", "plan_rule": [], "wifi_mode": "0", "ip": "192.168.99.100", "type": "1", "is_cur_host": "0", "cfg_valid": "0", "blocked": "0", "down_limit": "0", "down_speed": "0", "hostname": "Unknown", "up_limit": "0"}}, {"host_info_2": {"mac": "3C-0A-F3-D3-54-43", "up_speed": "0", "ssid": "LMD326", "plan_rule": [], "wifi_mode": "0", "ip": "192.168.99.101", "type": "1", "is_cur_host": "0", "cfg_valid": "0", "blocked": "0", "down_limit": "0", "down_speed": "0", "hostname": "honor", "up_limit": "0"}}]}, "ip_mac_bind": {"sys_arp": [{"sys_arp_1": {"status": 0, "hostname": "匿名主机", "mac": "4C-CC-6A-4A-EE-4A", "ip": "192.168.1.220"}}, {"sys_arp_2": {"hostname": "honor", "mac": "3C-0A-F3-D3-54-43", "ip": "192.168.99.101"}}, {"sys_arp_3": {"hostname": "Unknown", "mac": "B6-95-34-C0-8D-4A", "ip": "192.168.99.100"}}, {"sys_arp_4": {"hostname": "匿名主机", "mac": "00-0C-29-17-C5-D8", "ip": "192.168.1.4"}}, {"sys_arp_5": {"hostname": "匿名主机", "mac": "A8-A1-59-1E-53-02", "ip": "192.168.1.227"}}, {"sys_arp_6": {"hostname": "匿名主机", "mac": "74-7D-24-36-7A-29", "ip": "192.168.1.1"}}]}, "network": {"lan": {".name": "lan", "proto": "static", "stp": "0", "netmask": "255.255.255.0", "ip_mode": "manual", "fac_ipaddr": "192.168.1.1", "ifname": "eth1", ".type": "interface", "type": "bridge", "macaddr": "88-25-93-75-6F-81", ".anonymous": false, "ipaddr": "192.168.99.1"}}, "error_code": 0}`

func TestEncodePassword(t *testing.T) {
	// 与 DESIGN.md §2.3 的混淆算法一致；此处用测试专用假密码，
	// 真实路由器密码只通过环境变量注入，不写入仓库。
	const want = "t7zU2drULeefbwK"
	if got := EncodePassword("DevPass123"); got != want {
		t.Fatalf("EncodePassword mismatch: got %q want %q", got, want)
	}
}

func TestLoginWithEncodedPassword(t *testing.T) {
	const encodedPassword = "test-encoded-password"
	var receivedPassword string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Login struct {
				Password string `json:"password"`
			} `json:"login"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		receivedPassword = body.Login.Password
		_, _ = w.Write([]byte(`{"stok":"test-stok","error_code":0}`))
	}))
	t.Cleanup(srv.Close)

	a := New("tplink-1", srv.URL, encodedPassword, true, 5*time.Second, nil)
	if _, err := a.loginOnce(context.Background()); err != nil {
		t.Fatalf("loginOnce: %v", err)
	}
	if receivedPassword != encodedPassword {
		t.Fatalf("encoded password changed: got %q want %q", receivedPassword, encodedPassword)
	}
}

func newTestServer(t *testing.T, loginBody string, loginHits *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		if r.URL.Path == "/" {
			*loginHits++
			_, _ = w.Write([]byte(loginBody))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/stok=") {
			_, _ = w.Write([]byte(rawResponse))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCollectHappyPath(t *testing.T) {
	var loginHits int
	srv := newTestServer(t, `{"stok":"test-stok","error_code":0}`, &loginHits)
	a := New("tplink-1", srv.URL, "DevPass123", false, 5*time.Second, nil)

	ctx := context.Background()
	obs1, err := a.Collect(ctx)
	if err != nil {
		t.Fatalf("collect 1: %v", err)
	}
	// stok 复用：第二轮不应再登录
	obs2, err := a.Collect(ctx)
	if err != nil {
		t.Fatalf("collect 2: %v", err)
	}
	if loginHits != 1 {
		t.Fatalf("expected exactly 1 login (stok reuse), got %d", loginHits)
	}
	if !a.Healthy() {
		t.Fatal("adapter should be healthy after success")
	}

	check := func(obs []source.Observation) {
		if len(obs) != 6 {
			t.Fatalf("expected 6 devices, got %d: %+v", len(obs), obs)
		}
		by := map[string]source.Observation{}
		for _, o := range obs {
			by[o.MAC] = o
		}
		// host_info 权威在线设备
		honor := by["3C-0A-F3-D3-54-43"]
		if honor.MAC == "" {
			t.Fatal("missing honor device")
		}
		if !honor.Present || honor.Confidence != 85 || honor.Hostname != "honor" || honor.SSID != "LMD326" || honor.LinkType != source.LinkWifi {
			t.Fatalf("honor wrong: %+v", honor)
		}
		unk := by["B6-95-34-C0-8D-4A"]
		if !unk.Present || unk.Confidence != 85 || unk.Hostname != "Unknown" {
			t.Fatalf("Unknown device wrong: %+v", unk)
		}
		// WAN 侧 sys_arp：匿名主机应清空，且非在线、confidence=25
		wan := by["74-7D-24-36-7A-29"]
		if wan.MAC == "" {
			t.Fatal("missing WAN device 74-7D-24-36-7A-29")
		}
		if wan.Present || wan.Confidence != 25 || wan.Hostname != "" {
			t.Fatalf("WAN device wrong: %+v", wan)
		}
	}
	check(obs1)
	check(obs2)
}

func TestCollectSysLocked(t *testing.T) {
	var mu sync.Mutex
	var loginHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		if r.URL.Path == "/" {
			mu.Lock()
			loginHits++
			mu.Unlock()
		}
		_, _ = w.Write([]byte(`{"error_code":-40404}`))
	}))
	t.Cleanup(srv.Close)

	a := New("tplink-1", srv.URL, "DevPass123", false, 5*time.Second, nil)
	ctx := context.Background()

	_, err1 := a.Collect(ctx)
	if err1 == nil {
		t.Fatal("expected error on ESYSLOCKED login")
	}
	if a.Healthy() {
		t.Fatal("adapter should be unhealthy after lock")
	}
	// 锁定窗口内再次采集不应再打登录接口
	_, err2 := a.Collect(ctx)
	if err2 == nil {
		t.Fatal("expected error while locked")
	}

	mu.Lock()
	got := loginHits
	mu.Unlock()
	if got != 1 {
		t.Fatalf("expected 1 login attempt then lock (no retry), got %d", got)
	}
}
