package tplink

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// 错误码（见 docs/DESIGN.md 附录 A）。
var (
	errSysLocked = fmt.Errorf("tplink system locked (ESYSLOCKED -40404)")
	errUnauth    = fmt.Errorf("tplink unauthenticated (EUNAUTH -40401)")
)

// loginLockDuration TP-Link 密码错 10 次锁定 20 分钟（DESIGN §2.3 / §6.4）。
const loginLockDuration = 20 * time.Minute

// post 发送 JSON POST，返回原始响应体与解析出的 error_code。
func (a *Adapter) post(ctx context.Context, path string, body interface{}) ([]byte, int, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}
	var env struct {
		ErrorCode int `json:"error_code"`
	}
	// 解析失败不致命：保留原始体，error_code 视为 -1
	if err := json.Unmarshal(raw, &env); err != nil {
		return raw, -1, nil
	}
	return raw, env.ErrorCode, nil
}

// loginOnce 执行一次登录，成功返回 stok。-40404 返回 errSysLocked，其它非 0 返回错误。
func (a *Adapter) loginOnce(ctx context.Context) (string, error) {
	body := map[string]interface{}{
		"method": "do",
		"login":  map[string]string{"password": EncodePassword(a.password)},
	}
	raw, ec, err := a.post(ctx, "/", body)
	if err != nil {
		return "", err
	}
	if ec == -40404 {
		return "", errSysLocked
	}
	if ec != 0 {
		return "", fmt.Errorf("tplink login error_code=%d", ec)
	}
	var r struct {
		Stok string `json:"stok"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", fmt.Errorf("tplink login: decode stok: %w", err)
	}
	if r.Stok == "" {
		return "", fmt.Errorf("tplink login: empty stok")
	}
	return r.Stok, nil
}

// ds 取数：一次 POST 并行取 host_info / sys_arp / network。
func (a *Adapter) ds(ctx context.Context, stok string) ([]byte, error) {
	body := map[string]interface{}{
		"method":      "get",
		"hosts_info":  map[string]string{"table": "device", "name": "host_info"},
		"ip_mac_bind": map[string]string{"name": "sys_arp"},
		"network":     map[string]string{"name": "lan"},
	}
	raw, ec, err := a.post(ctx, "/stok="+stok+"/ds", body)
	if err != nil {
		return nil, err
	}
	switch ec {
	case 0:
		return raw, nil
	case -40401:
		return nil, errUnauth
	case -40404:
		return nil, errSysLocked
	default:
		return nil, fmt.Errorf("tplink ds error_code=%d", ec)
	}
}

// backoffFor 返回第 n 次连续失败后的退避时长：60s -> 120s -> 300s（封顶）。
func backoffFor(fails int) time.Duration {
	switch {
	case fails <= 1:
		return 60 * time.Second
	case fails == 2:
		return 120 * time.Second
	default:
		return 300 * time.Second
	}
}
