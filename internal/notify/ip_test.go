package notify

import (
	"strings"
	"testing"
	"time"

	"github.com/ider-zh/lanwatch/internal/config"
	"github.com/ider-zh/lanwatch/internal/presence"
	"github.com/ider-zh/lanwatch/internal/store"
)

func TestNotificationIPPreservesEventAddress(t *testing.T) {
	engine, st, stop := newTestEngine(t, config.NotifyConfig{})
	defer stop()
	const mac = "AA-BB-CC-DD-EE-01"
	if err := st.UpsertDevice(store.Device{MAC: mac, CurrentIP: "192.168.1.42"}); err != nil {
		t.Fatal(err)
	}
	const eventIP = "192.168.1.99"
	data := engine.toData(presence.Event{Type: "ip_changed", MAC: mac, IP: eventIP})
	if data.Device.IP != eventIP {
		t.Fatalf("notification IP = %q, want event IP %q", data.Device.IP, eventIP)
	}
}

func TestNotificationIPWithoutStoredDevice(t *testing.T) {
	engine, _, stop := newTestEngine(t, config.NotifyConfig{})
	defer stop()
	for _, ip := range []string{"192.168.1.99", ""} {
		data := engine.toData(presence.Event{Type: "online", MAC: "AA-BB-CC-DD-EE-02", IP: ip})
		if data.Device.IP != ip {
			t.Errorf("notification IP = %q, want %q without stored device", data.Device.IP, ip)
		}
	}
}

func TestHandledNotificationPayloadIncludesStoredIP(t *testing.T) {
	for _, channelType := range []string{"dingtalk", "email"} {
		t.Run(channelType, func(t *testing.T) {
			engine, st, stop := newTestEngine(t, config.NotifyConfig{RateLimitPerChannel: 100})
			defer stop()
			const mac = "AA-BB-CC-DD-EE-01"
			const ip = "192.168.1.42"
			if err := st.UpsertDevice(store.Device{MAC: mac, CurrentIP: ip}); err != nil {
				t.Fatal(err)
			}
			if err := st.SetSubscription(mac, 1, `["offline"]`); err != nil {
				t.Fatal(err)
			}
			sender := &fakeSender{id: 1}
			engine.RegisterSender(1, channelType, sender)
			engine.Handle(presence.Event{Type: "offline", MAC: mac, At: time.Now().Unix()})
			if !waitFor(t, func() bool {
				logs, err := st.ListNotifyLogs(10, 0, false)
				return err == nil && len(logs) == 1 && logs[0].Status == "sent"
			}, 3*time.Second) {
				t.Fatal("expected completed notification")
			}
			count, items := sender.snapshot()
			if count != 1 || len(items) != 1 || items[0].Device.IP != ip {
				t.Fatalf("sender did not receive device IP: count=%d, items=%+v", count, items)
			}
			logs, err := st.ListNotifyLogs(10, 0, false)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(logs[0].Payload, ip) {
				t.Fatalf("audited notification payload does not include IP %q: %s", ip, logs[0].Payload)
			}
		})
	}
}
func TestEventNotificationsIncludeStoredIP(t *testing.T) {
	for _, eventType := range []string{"online", "offline", "flapping", "hostname_changed"} {
		t.Run(eventType, func(t *testing.T) {
			engine, st, stop := newTestEngine(t, config.NotifyConfig{})
			defer stop()
			const mac = "AA-BB-CC-DD-EE-01"
			const ip = "192.168.1.42"
			if err := st.UpsertDevice(store.Device{MAC: mac, CurrentIP: ip, Hostname: "phone"}); err != nil {
				t.Fatal(err)
			}
			data := engine.toData(presence.Event{Type: eventType, MAC: mac, At: time.Now().Unix()})
			if data.Device.IP != ip {
				t.Fatalf("notification IP = %q, want stored device IP %q", data.Device.IP, ip)
			}
			_, markdown, err := renderDingtalk(data)
			if err != nil {
				t.Fatal(err)
			}
			html, err := renderEmailHTML(data)
			if err != nil {
				t.Fatal(err)
			}
			for name, rendered := range map[string]string{"dingtalk": markdown, "email": html} {
				if !strings.Contains(rendered, ip) {
					t.Errorf("%s notification does not include device IP %q: %s", name, ip, rendered)
				}
			}
		})
	}
}
