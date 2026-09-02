package notify

import (
	"context"
	"fmt"
	"time"

	"github.com/ider-zh/lanwatch/internal/store"
)

// EmailConfig 邮件渠道明文配置（仅内存短期存在，落库加密）。
type EmailConfig struct {
	Host     string   `json:"host"`
	Port     string   `json:"port"`
	Username string   `json:"username"`
	Password string   `json:"password"`
	From     string   `json:"from"`
	To       []string `json:"to"`
	Cc       []string `json:"cc"`
	TLS      string   `json:"tls"` // ssl | starttls | none
	Subject  string   `json:"subject"`
}

// DingtalkConfig 钉钉渠道明文配置。
type DingtalkConfig struct {
	Webhook   string   `json:"webhook"`
	Secret    string   `json:"secret"`
	MsgType   string   `json:"msgtype"` // markdown | text
	AtAll     bool     `json:"at_all"`
	AtMobiles []string `json:"at_mobiles"`
}

// Sender 发送一条已渲染的数据到具体渠道。
type Sender interface {
	Send(ctx context.Context, data TemplateData) error
	// SendDigest 把多条消息合并为一条摘要发送（限流触发时调用）。
	SendDigest(ctx context.Context, items []TemplateData) error
	// ChannelID 便于引擎做限流/日志关联。
	ChannelID() int64
}

type baseSender struct {
	id int64
}

func (b baseSender) ChannelID() int64 { return b.id }

// NewSender 解密渠道配置并构造对应 Sender。
func NewSender(ch store.Channel, c *Crypter) (Sender, error) {
	switch ch.Type {
	case "email":
		var cfg EmailConfig
		if err := c.DecryptJSON(ch.ConfigEnc, &cfg); err != nil {
			return nil, fmt.Errorf("decrypt email config: %w", err)
		}
		return &emailSender{baseSender: baseSender{id: ch.ID}, cfg: cfg}, nil
	case "dingtalk":
		var cfg DingtalkConfig
		if err := c.DecryptJSON(ch.ConfigEnc, &cfg); err != nil {
			return nil, fmt.Errorf("decrypt dingtalk config: %w", err)
		}
		if cfg.MsgType == "" {
			cfg.MsgType = "markdown"
		}
		return &dingtalkSender{baseSender: baseSender{id: ch.ID}, cfg: cfg}, nil
	default:
		return nil, fmt.Errorf("unknown channel type %q", ch.Type)
	}
}

// DeviceView 模板中的设备视图。
type DeviceView struct {
	MAC, IP, Hostname, Name, Vendor, LinkType, SSID string
}

func (d DeviceView) NameOrHost() string {
	if d.Name != "" {
		return d.Name
	}
	if d.Hostname != "" {
		return d.Hostname
	}
	return d.MAC
}

var linkTypeCN = map[string]string{
	"wired":      "有线",
	"wifi":       "Wi-Fi",
	"wifi2g":     "Wi-Fi 2.4G",
	"wifi5g":     "Wi-Fi 5G",
	"wifi-guest": "访客 Wi-Fi",
	"guest":      "访客",
	"unknown":    "未知",
}

func (d DeviceView) LinkTypeCN() string {
	if v, ok := linkTypeCN[d.LinkType]; ok {
		return v
	}
	return d.LinkType
}

// EventView 模板中的事件视图。
type EventView struct {
	Type string
	At   time.Time
}

func (e EventView) TypeCN() string {
	switch e.Type {
	case "online":
		return "设备上线"
	case "offline":
		return "设备离线"
	case "new_device":
		return "新设备接入"
	case "ip_changed":
		return "IP 变更"
	case "hostname_changed":
		return "主机名变更"
	case "flapping":
		return "抖动告警"
	default:
		return e.Type
	}
}

// TemplateData 渲染模板时的数据上下文（见 DESIGN 附录 E）。
type TemplateData struct {
	Device     DeviceView
	Event      EventView
	Duration   string
	LastSeen   string
	SourceName string
	BaseURL    string
	Flapping   bool
}
