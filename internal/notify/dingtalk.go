package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type dingtalkSender struct {
	baseSender
	cfg DingtalkConfig
}

// DingtalkSign 计算钉钉加签（官方算法）：sign = urlencode(base64(HMAC_SHA256(ts+"\n"+secret)))。
func DingtalkSign(secret string) (timestamp, sign string) {
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "\n" + secret))
	sign = url.QueryEscape(base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	return ts, sign
}

func (s *dingtalkSender) signURL() string {
	ts, sign := DingtalkSign(s.cfg.Secret)
	sep := "?"
	if strings.Contains(s.cfg.Webhook, "?") {
		sep = "&"
	}
	return s.cfg.Webhook + sep + "timestamp=" + ts + "&sign=" + sign
}

func (s *dingtalkSender) post(ctx context.Context, title, text string) error {
	at := map[string]interface{}{
		"atMobiles": s.cfg.AtMobiles,
		"isAtAll":   s.cfg.AtAll,
	}
	var payload map[string]interface{}
	switch s.cfg.MsgType {
	case "text":
		payload = map[string]interface{}{
			"msgtype": "text",
			"text":    map[string]string{"content": text},
			"at":      at,
		}
	default: // markdown
		payload = map[string]interface{}{
			"msgtype":  "markdown",
			"markdown": map[string]string{"title": title, "text": text},
			"at":       at,
		}
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.signURL(), strings.NewReader(string(b)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("dingtalk post: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("dingtalk decode: %w", err)
	}
	if out.ErrCode != 0 {
		return fmt.Errorf("dingtalk error %d: %s", out.ErrCode, out.ErrMsg)
	}
	return nil
}

func (s *dingtalkSender) Send(ctx context.Context, data TemplateData) error {
	title, text, err := renderDingtalk(data)
	if err != nil {
		return err
	}
	return s.post(ctx, title, text)
}

func (s *dingtalkSender) SendDigest(ctx context.Context, items []TemplateData) error {
	var b strings.Builder
	for i, it := range items {
		_, text, err := renderDingtalk(it)
		if err != nil {
			return err
		}
		if i > 0 {
			b.WriteString("\n\n---\n\n")
		}
		b.WriteString(text)
	}
	return s.post(ctx, fmt.Sprintf("lanwatch 通知摘要（%d 条）", len(items)), b.String())
}
