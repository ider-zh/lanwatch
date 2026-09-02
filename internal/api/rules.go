package api

import (
	"encoding/json"
	"net/http"

	"github.com/ider-zh/lanwatch/internal/store"
)

// ruleReq 创建/更新规则的请求体。events 以数组传入，落库为 JSON 串。
type ruleReq struct {
	Name          string   `json:"name"`
	Enabled       bool     `json:"enabled"`
	Scope         string   `json:"scope"`     // all | devices | new_devices
	ScopeRef      string   `json:"scope_ref"` // JSON 串（devices 时为 mac 数组）
	Events        []string `json:"events"`    // 事件数组
	MinOfflineSec int      `json:"min_offline_sec"`
	QuietHours    string   `json:"quiet_hours"` // JSON 串
	CooldownSec   int      `json:"cooldown_sec"`
	DigestSec     int      `json:"digest_sec"`
	ChannelIDs    []int64  `json:"channel_ids"`
}

// ruleOut 规则对外视图（events 还原为数组，附 channel_ids）。
type ruleOut struct {
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	Enabled       bool     `json:"enabled"`
	Scope         string   `json:"scope"`
	ScopeRef      string   `json:"scope_ref"`
	Events        []string `json:"events"`
	MinOfflineSec int      `json:"min_offline_sec"`
	QuietHours    string   `json:"quiet_hours"`
	CooldownSec   int      `json:"cooldown_sec"`
	DigestSec     int      `json:"digest_sec"`
	ChannelIDs    []int64  `json:"channel_ids"`
	CreatedAt     int64    `json:"created_at"`
}

func (a *API) handleListRules(w http.ResponseWriter, r *http.Request) {
	rules, err := a.st.ListRules()
	if err != nil {
		logCtx(a.log, r).Error("list rules", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	out := make([]ruleOut, 0, len(rules))
	for _, rl := range rules {
		out = append(out, a.ruleToOut(rl))
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": out})
}

func (a *API) handleCreateRule(w http.ResponseWriter, r *http.Request) {
	var req ruleReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	events, err := json.Marshal(req.Events)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid events")
		return
	}
	id, err := a.st.CreateRule(req.Name, req.Enabled, req.Scope, req.ScopeRef, string(events),
		req.QuietHours, req.MinOfflineSec, req.CooldownSec, req.DigestSec)
	if err != nil {
		logCtx(a.log, r).Error("create rule", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := a.st.SetRuleChannels(id, req.ChannelIDs); err != nil {
		logCtx(a.log, r).Error("set rule channels", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "name": req.Name})
}

func (a *API) handleUpdateRule(w http.ResponseWriter, r *http.Request) {
	id := chiURLInt64(r, "id")
	if _, err := a.st.GetRule(id); err != nil {
		writeError(w, http.StatusNotFound, "rule not found")
		return
	}
	var req ruleReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	events, err := json.Marshal(req.Events)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid events")
		return
	}
	if err := a.st.UpdateRule(id, req.Name, req.Enabled, req.Scope, req.ScopeRef, string(events),
		req.QuietHours, req.MinOfflineSec, req.CooldownSec, req.DigestSec); err != nil {
		logCtx(a.log, r).Error("update rule", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := a.st.SetRuleChannels(id, req.ChannelIDs); err != nil {
		logCtx(a.log, r).Error("set rule channels", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	id := chiURLInt64(r, "id")
	if err := a.st.DeleteRule(id); err != nil {
		logCtx(a.log, r).Error("delete rule", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) ruleToOut(rl store.NotifyRule) ruleOut {
	out := ruleOut{
		ID:            rl.ID,
		Name:          rl.Name,
		Enabled:       rl.Enabled,
		Scope:         rl.Scope,
		ScopeRef:      rl.ScopeRef,
		MinOfflineSec: rl.MinOfflineSec,
		QuietHours:    rl.QuietHours,
		CooldownSec:   rl.CooldownSec,
		DigestSec:     rl.DigestSec,
		CreatedAt:     rl.CreatedAt,
		Events:        []string{},
		ChannelIDs:    []int64{},
	}
	if rl.Events != "" {
		_ = json.Unmarshal([]byte(rl.Events), &out.Events)
	}
	if cids, err := a.st.ListRuleChannelIDs(rl.ID); err == nil {
		out.ChannelIDs = cids
	}
	return out
}
