package notify

import (
	"testing"
	"time"

	"github.com/ider-zh/lanwatch/internal/config"
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
