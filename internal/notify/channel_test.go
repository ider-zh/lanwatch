package notify

import (
	"strings"
	"testing"
	"time"

	"github.com/ider-zh/lanwatch/internal/config"
	"github.com/ider-zh/lanwatch/internal/presence"
	"github.com/ider-zh/lanwatch/internal/store"
)

func atTime() time.Time { return time.Date(2026, 9, 2, 8, 0, 0, 0, time.Local) }

func sampleData() TemplateData {
	return TemplateData{
		Device:     DeviceView{MAC: "AA-BB-CC-DD-EE-FF", IP: "192.168.1.50", Hostname: "phone", Vendor: "Apple", LinkType: "wifi", SSID: "HOME"},
		Event:      EventView{Type: "offline", At: atTime()},
		Duration:   "5m",
		LastSeen:   "08:00:00",
		SourceName: "asus-1",
		BaseURL:    "http://localhost:8080",
	}
}

func TestDingtalkSignFormat(t *testing.T) {
	ts, sign := DingtalkSign("SECtest")
	if len(ts) != 13 {
		t.Fatalf("timestamp should be 13 digits, got %q", ts)
	}
	if sign == "" {
		t.Fatal("sign empty")
	}
	// 同一瞬间可复现
	ts2, sign2 := DingtalkSign("SECtest")
	if ts != ts2 || sign != sign2 {
		t.Fatal("sign not reproducible within same instant")
	}
}

func TestRenderDingtalk(t *testing.T) {
	title, text, err := renderDingtalk(sampleData())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "设备离线") {
		t.Fatalf("expected offline title, got %q", title)
	}
	for _, want := range []string{"phone", "192.168.1.50", "AA-BB-CC-DD-EE-FF", "Apple", "连续未检测到", "最后确认在线", "5m", "asus-1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("dingtalk text missing %q:\n%s", want, text)
		}
	}
}

func TestRenderEmail(t *testing.T) {
	subj := renderEmailSubject(sampleData())
	if !strings.Contains(subj, "设备离线") || !strings.Contains(subj, "phone") {
		t.Fatalf("subject wrong: %q", subj)
	}
	html, err := renderEmailHTML(sampleData())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"AA-BB-CC-DD-EE-FF", "192.168.1.50", "/timeline"} {
		if !strings.Contains(html, want) {
			t.Fatalf("email html missing %q", want)
		}
	}
}

func TestOfflineTemplatesWithoutObservationTime(t *testing.T) {
	data := sampleData()
	data.Duration = "1m0s"
	data.LastSeen = ""
	_, markdown, err := renderDingtalk(data)
	if err != nil {
		t.Fatal(err)
	}
	html, err := renderEmailHTML(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, rendered := range []string{markdown, html} {
		if !strings.Contains(rendered, "连续未检测到") || !strings.Contains(rendered, "1m0s") {
			t.Fatalf("missing observation duration: %s", rendered)
		}
		for _, forbidden := range []string{"已离线", "持续离线", "最后确认在线", "上次在线"} {
			if strings.Contains(rendered, forbidden) {
				t.Fatalf("misleading timing %q in %s", forbidden, rendered)
			}
		}
	}
}

// TestEventTimeInCST 验证 toData 把事件时间转换为中国标准时间（UTC+8）而非 UTC。
// 1970-01-01 00:00:00 UTC 应显示为 1970-01-01 08:00:00。
func TestEventTimeInCST(t *testing.T) {
	e, _, cleanup := newTestEngine(t, config.NotifyConfig{})
	defer cleanup()

	ev := presence.Event{Type: "online", MAC: "AA-BB-CC-DD-EE-FF", IP: "192.168.1.50", At: 0}
	d := e.toData(ev)
	got := d.Event.At.Format("2006-01-02 15:04:05")
	if got != "1970-01-01 08:00:00" {
		t.Fatalf("event time not in CST (+8h), got %q", got)
	}

	// 离线事件的上次在线时间也应转换为 CST（只取时分秒）。
	ev2 := presence.Event{Type: "offline", MAC: "AA-BB-CC-DD-EE-FF", IP: "192.168.1.50", At: 0, LastSeen: 1}
	d2 := e.toData(ev2)
	if d2.LastSeen != "08:00:01" {
		t.Fatalf("last seen not in CST (+8h), got %q", d2.LastSeen)
	}
}

func TestNewSenderDecrypt(t *testing.T) {
	c := NewCrypter("secret")
	enc, err := c.EncryptJSON(EmailConfig{Host: "smtp.x", Port: "465", TLS: "ssl", To: []string{"a@b.c"}})
	if err != nil {
		t.Fatal(err)
	}
	ch := store.Channel{ID: 7, Type: "email", ConfigEnc: enc}
	s, err := NewSender(ch, c)
	if err != nil {
		t.Fatal(err)
	}
	if s.ChannelID() != 7 {
		t.Fatalf("channel id mismatch: %d", s.ChannelID())
	}

	// dingtalk
	enc2, _ := c.EncryptJSON(DingtalkConfig{Webhook: "https://oapi/robot/send?access_token=x", Secret: "SEC"})
	ch2 := store.Channel{ID: 8, Type: "dingtalk", ConfigEnc: enc2}
	s2, err := NewSender(ch2, c)
	if err != nil {
		t.Fatal(err)
	}
	if s2.ChannelID() != 8 {
		t.Fatalf("dingtalk channel id mismatch: %d", s2.ChannelID())
	}
}
