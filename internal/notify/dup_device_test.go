package notify

import (
	"testing"
	"time"

	"github.com/ider-zh/lanwatch/internal/config"
	"github.com/ider-zh/lanwatch/internal/presence"
)

// 复现：短期内两个不同设备先后离线，第二个设备不应被冷却/限流吞掉。
func TestTwoDevicesQuickSuccession(t *testing.T) {
	e, st, stop := newTestEngine(t, config.NotifyConfig{CooldownSec: 300, RateLimitPerChannel: 15})
	defer stop()
	f := &fakeSender{id: 1}
	e.RegisterSender(1, "dingtalk", f)

	mac1 := "AA-BB-CC-DD-EE-01"
	mac2 := "AA-BB-CC-DD-EE-02"
	if err := st.SetSubscription(mac1, 1, `["offline"]`); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSubscription(mac2, 1, `["offline"]`); err != nil {
		t.Fatal(err)
	}

	e.Handle(offlineEvent(mac1))
	e.Handle(offlineEvent(mac2)) // 另一台设备，独立冷却键，应正常发送

	if !waitFor(t, func() bool {
		n, _ := f.snapshot()
		return n >= 2
	}, 3*time.Second) {
		n, items := f.snapshot()
		macs := []string{}
		for _, it := range items {
			macs = append(macs, it.Device.MAC)
		}
		t.Fatalf("expected 2 sends (one per device), got %d; sent macs=%v", n, macs)
	}
}

func TestOverlappingSubscriptionsAndRulesSendOncePerChannel(t *testing.T) {
	for _, eventType := range []string{"online", "offline"} {
		t.Run(eventType, func(t *testing.T) {
			engine, st, stop := newTestEngine(t, config.NotifyConfig{RateLimitPerChannel: 100})
			defer stop()
			const mac = "AA-BB-CC-DD-EE-01"
			if err := st.SetSubscription(mac, 1, `["online","offline"]`); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"first", "second"} {
				ruleID, err := st.CreateRule(name, true, "all", "", `["online","offline"]`, "", 0, 0, 0)
				if err != nil {
					t.Fatal(err)
				}
				if err := st.SetRuleChannels(ruleID, []int64{1, 2}); err != nil {
					t.Fatal(err)
				}
			}
			first, second := &fakeSender{id: 1}, &fakeSender{id: 2}
			engine.RegisterSender(1, "dingtalk", first)
			engine.RegisterSender(2, "email", second)
			event := presence.Event{Type: eventType, MAC: mac, At: time.Now().Unix()}
			if candidates := engine.expand(event); len(candidates) != 2 {
				t.Fatalf("expected one candidate per channel, got %d", len(candidates))
			}
			engine.Handle(event)
			if !waitFor(t, func() bool {
				logs, err := st.ListNotifyLogs(10, 0, false)
				return err == nil && len(logs) == 2 && logs[0].Status == "sent" && logs[1].Status == "sent"
			}, 3*time.Second) {
				t.Fatal("expected two completed sends")
			}
			for _, sender := range []*fakeSender{first, second} {
				if count, _ := sender.snapshot(); count != 1 {
					t.Fatalf("channel %d sent %d times", sender.id, count)
				}
			}
		})
	}
}

func TestOverlappingMatchesUseLongestChannelCooldown(t *testing.T) {
	engine, st, stop := newTestEngine(t, config.NotifyConfig{CooldownSec: 300})
	defer stop()
	const mac = "AA-BB-CC-DD-EE-01"
	if err := st.SetSubscription(mac, 1, `["offline"]`); err != nil {
		t.Fatal(err)
	}
	ruleID, err := st.CreateRule("long cooldown", true, "all", "", `["offline"]`, "", 0, 600, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetRuleChannels(ruleID, []int64{1, 2}); err != nil {
		t.Fatal(err)
	}
	candidates := engine.expand(offlineEvent(mac))
	if len(candidates) != 2 || candidates[0].cooldownSec != 600 || candidates[1].cooldownSec != 600 || candidates[0].cooldownKey == candidates[1].cooldownKey {
		t.Fatalf("incorrect channel cooldowns: %+v", candidates)
	}
}
