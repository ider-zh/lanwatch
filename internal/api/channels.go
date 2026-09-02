package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ider-zh/lanwatch/internal/notify"
	"github.com/ider-zh/lanwatch/internal/store"
)

// channelReq 创建/更新渠道的请求体。config 为明文 JSON（落库前加密）。
type channelReq struct {
	Type    string          `json:"type"`
	Name    string          `json:"name"`
	Enabled bool            `json:"enabled"`
	Config  json.RawMessage `json:"config"`
}

// channelOut 渠道对外视图（凭据已脱敏）。
type channelOut struct {
	ID        int64                  `json:"id"`
	Type      string                 `json:"type"`
	Name      string                 `json:"name"`
	Enabled   bool                   `json:"enabled"`
	Config    map[string]interface{} `json:"config"`
	CreatedAt int64                  `json:"created_at"`
}

func (a *API) handleListChannels(w http.ResponseWriter, r *http.Request) {
	chs, err := a.st.ListChannels()
	if err != nil {
		logCtx(a.log, r).Error("list channels", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	out := make([]channelOut, 0, len(chs))
	for _, c := range chs {
		out = append(out, a.channelToOut(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": out})
}

func (a *API) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	var req channelReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Type != "email" && req.Type != "dingtalk" {
		writeError(w, http.StatusBadRequest, "type must be email or dingtalk")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	enc, err := a.encryptConfig(req.Type, req.Config, nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid config: "+err.Error())
		return
	}
	id, err := a.st.CreateChannel(req.Type, req.Name, enc, req.Enabled)
	if err != nil {
		logCtx(a.log, r).Error("create channel", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if a.notify != nil {
		a.notify.LoadSenders()
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "type": req.Type, "name": req.Name, "enabled": req.Enabled})
}

func (a *API) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	id := chiURLInt64(r, "id")
	ch, err := a.st.GetChannel(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	var req channelReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	enc := ch.ConfigEnc
	if len(req.Config) > 0 && string(req.Config) != "null" {
		enc, err = a.encryptConfig(req.Type, req.Config, ch.ConfigEnc)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid config: "+err.Error())
			return
		}
	}
	if err := a.st.UpdateChannel(id, req.Name, enc, req.Enabled); err != nil {
		logCtx(a.log, r).Error("update channel", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if a.notify != nil {
		a.notify.LoadSenders()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	id := chiURLInt64(r, "id")
	if err := a.st.DeleteChannel(id); err != nil {
		logCtx(a.log, r).Error("delete channel", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if a.notify != nil {
		a.notify.LoadSenders()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleTestChannel 用样例事件实测一次渠道发送，返回耗时。
func (a *API) handleTestChannel(w http.ResponseWriter, r *http.Request) {
	id := chiURLInt64(r, "id")
	ch, err := a.st.GetChannel(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	var req struct {
		Event string `json:"event"`
		MAC   string `json:"mac"`
	}
	_ = decodeJSON(r, &req)
	event := req.Event
	if event == "" {
		event = "offline"
	}
	data := a.buildNotifyData(req.MAC, event)

	sender, err := notify.NewSender(*ch, a.crypter)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	start := time.Now()
	err = sender.Send(ctx, data)
	elapsed := time.Since(start)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "elapsed_ms": elapsed.Milliseconds()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "elapsed_ms": elapsed.Milliseconds()})
}

// encryptConfig 把明文 config 加密；oldEnc 非空时在合并前对 "***" 哨兵做保留处理。
func (a *API) encryptConfig(typ string, raw, oldEnc []byte) ([]byte, error) {
	var merged map[string]interface{}
	if err := json.Unmarshal(raw, &merged); err != nil {
		return nil, err
	}
	if len(oldEnc) > 0 && a.crypter != nil {
		if old, err := a.crypter.DecryptJSONToMap(oldEnc); err == nil {
			sentinel := "***"
			if typ == "email" {
				if v, ok := merged["password"]; ok && v == sentinel {
					if ov, ok2 := old["password"]; ok2 {
						merged["password"] = ov
					}
				}
			}
			if typ == "dingtalk" {
				if v, ok := merged["secret"]; ok && v == sentinel {
					if ov, ok2 := old["secret"]; ok2 {
						merged["secret"] = ov
					}
				}
			}
		}
	}
	return a.crypter.EncryptJSON(merged)
}

// channelToOut 构造对外视图：解密配置并脱敏 secret/password。
func (a *API) channelToOut(c store.Channel) channelOut {
	out := channelOut{
		ID:        c.ID,
		Type:      c.Type,
		Name:      c.Name,
		Enabled:   c.Enabled,
		CreatedAt: c.CreatedAt,
		Config:    map[string]interface{}{},
	}
	if a.crypter != nil && len(c.ConfigEnc) > 0 {
		if m, err := a.crypter.DecryptJSONToMap(c.ConfigEnc); err == nil {
			if c.Type == "email" {
				if _, ok := m["password"]; ok {
					m["password"] = "***"
				}
			}
			if c.Type == "dingtalk" {
				if _, ok := m["secret"]; ok {
					m["secret"] = "***"
				}
			}
			out.Config = m
		}
	}
	return out
}

// chiURLInt64 解析路径参数里的整型 id。
func chiURLInt64(r *http.Request, param string) int64 {
	return atoi64(chi.URLParam(r, param))
}
