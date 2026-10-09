package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ider-zh/lanwatch/internal/store"
)

func (a *API) handleHealth(w http.ResponseWriter, r *http.Request) {
	lr, err := a.st.LastRoundAt()
	healthy := true
	sources, serr := a.st.ListSourceStates()
	if serr == nil {
		for _, s := range sources {
			if !s.Healthy && s.ConsecutiveFail >= 3 {
				healthy = false
				break
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            err == nil,
		"last_round_at": lr,
		"db_ok":         err == nil,
		"all_healthy":   healthy,
	})
}

func (a *API) handleDevices(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.DeviceFilter{
		WatchedOnly:      q.Get("watched") == "true",
		Q:                q.Get("q"),
		Status:           q.Get("status"),
		HideFixedServers: q.Get("hide_fixed_servers") == "true" || q.Get("hide_fixed_servers") == "1",
	}
	if v := q.Get("online"); v != "" {
		b := v == "true" || v == "1"
		f.Online = &b
	}
	devs, err := a.st.ListDeviceViews(f)
	if err != nil {
		logCtx(a.log, r).Error("list devices", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devs})
}

func (a *API) handlePatchDevice(w http.ResponseWriter, r *http.Request) {
	mac := strings.ToUpper(chi.URLParam(r, "mac"))
	var req struct {
		Name        string `json:"name"`
		Notes       string `json:"notes"`
		Ignore      *bool  `json:"ignored"`
		FixedServer *bool  `json:"fixed_server"`
		Watched     *bool  `json:"watched"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	device, err := a.st.GetDevice(mac)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if device == nil {
		writeError(w, http.StatusNotFound, "device not found")
		return
	}
	if req.Watched != nil {
		if _, err := a.st.Exec(`UPDATE devices SET watched=? WHERE mac=?`, *req.Watched, mac); err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	if err := a.st.SetDevice(mac, req.Name, req.Notes, req.Ignore, req.FixedServer); err != nil {
		logCtx(a.log, r).Error("patch device", "mac", mac, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) handleDeviceSessions(w http.ResponseWriter, r *http.Request) {
	mac := strings.ToUpper(chi.URLParam(r, "mac"))
	from, to := parseRange(r)
	sessions, err := a.st.ListSessions(mac, from, to)
	if err != nil {
		logCtx(a.log, r).Error("list sessions", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	out := make([]map[string]any, 0, len(sessions))
	for _, s := range sessions {
		m := map[string]any{"start": s.StartAt, "end": s.EndAt, "reason": s.EndReason}
		out = append(out, m)
	}
	writeJSON(w, http.StatusOK, map[string]any{"mac": mac, "sessions": out})
}

func (a *API) handleTimeline(w http.ResponseWriter, r *http.Request) {
	from, to := parseRange(r)
	macs := []string{}
	if v := r.URL.Query().Get("macs"); v != "" {
		for _, m := range strings.Split(v, ",") {
			m = strings.TrimSpace(m)
			if m != "" {
				macs = append(macs, strings.ToUpper(m))
			}
		}
	}

	var devs []store.Device
	var err error
	if len(macs) > 0 {
		devs = make([]store.Device, 0, len(macs))
		for _, m := range macs {
			d, e := a.st.GetDevice(m)
			if e != nil {
				err = e
				break
			}
			if d != nil {
				devs = append(devs, *d)
			}
		}
	} else {
		devs, err = a.st.ListDevices(store.DeviceFilter{})
	}
	if err != nil {
		logCtx(a.log, r).Error("timeline devices", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	type devOut struct {
		MAC      string           `json:"mac"`
		Name     string           `json:"name"`
		Hostname string           `json:"hostname"`
		IP       string           `json:"ip"`
		Online   bool             `json:"online"`
		Sessions []map[string]any `json:"sessions"`
	}
	out := make([]devOut, 0, len(devs))
	for _, d := range devs {
		sessions, e := a.st.ListSessions(d.MAC, from, to)
		if e != nil {
			logCtx(a.log, r).Error("timeline sessions", "err", e)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		ss := make([]map[string]any, 0, len(sessions))
		for _, s := range sessions {
			ss = append(ss, map[string]any{"start": s.StartAt, "end": s.EndAt})
		}
		online, _ := a.st.IsOnline(d.MAC)
		out = append(out, devOut{MAC: d.MAC, Name: d.Name, Hostname: d.Hostname, IP: d.CurrentIP, Online: online, Sessions: ss})
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "devices": out})
}

func (a *API) handleSources(w http.ResponseWriter, r *http.Request) {
	sources, err := a.st.ListSourceStates()
	if err != nil {
		logCtx(a.log, r).Error("list sources", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": sources})
}

func (a *API) handleStatsOverview(w http.ResponseWriter, r *http.Request) {
	online, _ := a.st.CountOnline()
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	newToday, _ := a.st.CountNewSince(today.Unix())
	sources, _ := a.st.ListSourceStates()
	allHealthy := true
	for _, s := range sources {
		if !s.Healthy && s.ConsecutiveFail >= 3 {
			allHealthy = false
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"online_now":  online,
		"new_today":   newToday,
		"all_healthy": allHealthy,
		"sources":     sources,
	})
}

// parseRange 解析 from/to（支持 unix 秒或 RFC3339），缺省为最近 24h。
func parseRange(r *http.Request) (int64, int64) {
	q := r.URL.Query()
	now := time.Now().Unix()
	to := now
	from := now - 86400
	if v := q.Get("from"); v != "" {
		if t, err := strconv.ParseInt(v, 10, 64); err == nil {
			from = t
		} else if tm, e := time.Parse(time.RFC3339, v); e == nil {
			from = tm.Unix()
		}
	}
	if v := q.Get("to"); v != "" {
		if t, err := strconv.ParseInt(v, 10, 64); err == nil {
			to = t
		} else if tm, e := time.Parse(time.RFC3339, v); e == nil {
			to = tm.Unix()
		}
	}
	if to < from {
		from, to = to, from
	}
	return from, to
}
