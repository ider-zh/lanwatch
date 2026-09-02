package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/ider-zh/lanwatch/internal/notify"
)

// handleListNotifications 分页/筛选发送日志。
func (a *API) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := atoi64(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset := atoi64(q.Get("offset"))
	onlyErrors := q.Get("errors") == "true" || q.Get("errors") == "1"
	rows, err := a.st.ListNotifyLogs(int(limit), int(offset), onlyErrors)
	if err != nil {
		logCtx(a.log, r).Error("list notify logs", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"id":           row.ID,
			"rule_id":      row.RuleID,
			"channel_id":   row.ChannelID,
			"channel_name": row.ChannelName,
			"channel_type": row.ChannelType,
			"mac":          row.MAC,
			"event":        row.Event,
			"status":       row.Status,
			"attempts":     row.Attempts,
			"error":        row.Error,
			"created_at":   row.CreatedAt,
			"sent_at":      row.SentAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": out})
}

// handleRetryNotification 对一条失败日志重新发送。
func (a *API) handleRetryNotification(w http.ResponseWriter, r *http.Request) {
	id := chiURLInt64(r, "id")
	log, err := a.st.GetNotifyLog(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "log not found")
		return
	}
	ch, err := a.st.GetChannel(log.ChannelID)
	if err != nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	sender, err := notify.NewSender(*ch, a.crypter)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	mac := ""
	if log.MAC != nil {
		mac = *log.MAC
	}
	data := a.buildNotifyData(mac, log.Event)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	sendErr := sender.Send(ctx, data)
	if sendErr != nil {
		status := "failed"
		attempts := log.Attempts + 1
		_ = a.st.UpdateNotifyLog(id, status, attempts, sendErr.Error(), nil)
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": sendErr.Error()})
		return
	}
	now := time.Now().Unix()
	_ = a.st.UpdateNotifyLog(id, "sent", log.Attempts+1, "", &now)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// buildNotifyData 由 mac + 事件类型构造一份模板数据（优先填充设备已知信息）。
func (a *API) buildNotifyData(mac, event string) notify.TemplateData {
	mac = strings.ToUpper(strings.TrimSpace(mac))
	d := notify.DeviceView{MAC: mac}
	if mac != "" {
		if dev, err := a.st.GetDevice(mac); err == nil && dev != nil {
			d.IP = dev.CurrentIP
			d.Hostname = dev.Hostname
			d.Name = dev.Name
			d.Vendor = dev.Vendor
			d.LinkType = dev.LinkType
			d.SSID = dev.SSID
		}
	}
	if d.Name == "" {
		d.Name = d.Hostname
	}
	return notify.TemplateData{
		Device:   d,
		Event:    notify.EventView{Type: event, At: time.Now()},
		BaseURL:  a.baseURL(),
		Flapping: event == "flapping",
	}
}

// baseURL 用于模板里跳转前端的链接前缀。
func (a *API) baseURL() string {
	addr := a.cfg.ListenAddr
	if addr == "" {
		return ""
	}
	if strings.HasPrefix(addr, ":") {
		return "http://127.0.0.1" + addr
	}
	return "http://" + addr
}
