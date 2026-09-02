package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/ider-zh/lanwatch/internal/notify"
)

// handleListSubscriptions 返回某设备的全部渠道订阅。
func (a *API) handleListSubscriptions(w http.ResponseWriter, r *http.Request) {
	mac := strings.ToUpper(chi.URLParam(r, "mac"))
	subs, err := a.st.ListSubscriptions(mac)
	if err != nil {
		logCtx(a.log, r).Error("list subscriptions", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	out := make([]map[string]any, 0, len(subs))
	for _, s := range subs {
		events := []string{}
		if s.Events != "" {
			_ = json.Unmarshal([]byte(s.Events), &events)
		}
		out = append(out, map[string]any{
			"channel_id": s.ChannelID,
			"events":     events,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"mac": mac, "subscriptions": out})
}

// handlePutSubscriptions 整体替换某设备的渠道订阅。
// 请求体：{"channels":[1,2], "events":["online","offline"]}。
// 对新增订阅的已启用渠道，即时发一条确认消息（best-effort）。
func (a *API) handlePutSubscriptions(w http.ResponseWriter, r *http.Request) {
	mac := strings.ToUpper(chi.URLParam(r, "mac"))
	var req struct {
		Channels []int64  `json:"channels"`
		Events   []string `json:"events"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	eventsJSON, err := json.Marshal(req.Events)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid events")
		return
	}

	// 记录替换前的订阅集合，用于判断是否新增渠道。
	before, _ := a.st.ListSubscriptions(mac)
	beforeSet := map[int64]bool{}
	for _, s := range before {
		beforeSet[s.ChannelID] = true
	}

	// 整体替换：先清后写。
	if err := a.st.ClearSubscriptions(mac); err != nil {
		logCtx(a.log, r).Error("clear subscriptions", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	for _, cid := range req.Channels {
		if err := a.st.SetSubscription(mac, cid, string(eventsJSON)); err != nil {
			logCtx(a.log, r).Error("set subscription", "err", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	// 对新增订阅的已启用渠道，即时发一条确认消息（best-effort，不阻塞请求）。
	if a.crypter != nil {
		for _, cid := range req.Channels {
			if beforeSet[cid] {
				continue
			}
			ch, err := a.st.GetChannel(cid)
			if err != nil || !ch.Enabled {
				continue
			}
			sender, err := notify.NewSender(*ch, a.crypter)
			if err != nil {
				continue
			}
			ev := "online"
			if len(req.Events) > 0 {
				ev = req.Events[0]
			}
			data := a.buildNotifyData(mac, ev)
			go func(s notify.Sender, d notify.TemplateData) {
				_ = s.Send(context.Background(), d)
			}(sender, data)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
