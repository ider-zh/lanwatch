package store

import (
	"database/sql"
	"encoding/json"
	"strings"
)

func (s *Store) GetDevice(mac string) (*Device, error) {
	var d Device
	var random int
	err := s.QueryRow(`SELECT mac, name, hostname, vendor, current_ip, link_type, ssid, status,
		likely_random_mac, first_seen, last_seen, notes, fixed_server FROM devices WHERE mac=?`, mac).
		Scan(&d.MAC, &d.Name, &d.Hostname, &d.Vendor, &d.CurrentIP, &d.LinkType, &d.SSID,
			&d.Status, &random, &d.FirstSeen, &d.LastSeen, &d.Notes, &d.FixedServer)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.LikelyRandomMAC = random == 1
	return &d, nil
}

// UpsertDevice 插入或合并更新。非空字段覆盖，first_seen 保留最早。
func (s *Store) UpsertDevice(d Device) error {
	ts := d.LastSeen
	exist, err := s.GetDevice(d.MAC)
	if err != nil {
		return err
	}
	if exist == nil {
		first := d.FirstSeen
		if first == 0 {
			first = ts
		}
		_, err := s.Exec(`INSERT INTO devices
			(mac, name, hostname, vendor, current_ip, link_type, ssid, status, likely_random_mac, first_seen, last_seen, notes, fixed_server)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			d.MAC, d.Name, d.Hostname, d.Vendor, d.CurrentIP, d.LinkType, d.SSID, d.Status,
			boolToInt(d.LikelyRandomMAC), first, ts, d.Notes, boolToInt(d.FixedServer))
		return err
	}
	// 仅用非空字段覆盖
	name, hostname, vendor, ip, link, ssid, status, notes := exist.Name, exist.Hostname, exist.Vendor, exist.CurrentIP, exist.LinkType, exist.SSID, exist.Status, exist.Notes
	if d.Name != "" {
		name = d.Name
	}
	if d.Hostname != "" {
		hostname = d.Hostname
	}
	if d.Vendor != "" {
		vendor = d.Vendor
	}
	if d.CurrentIP != "" {
		ip = d.CurrentIP
	}
	if d.LinkType != "" {
		link = d.LinkType
	}
	if d.SSID != "" {
		ssid = d.SSID
	}
	if d.Status != "" {
		status = d.Status
	}
	if d.Notes != "" {
		notes = d.Notes
	}
	// last_seen 仅在调用方显式传入非零值时更新（如进入在线时）；
	// 传 0 表示"不改动"，避免每次观测都把它刷成当前时间（否则"最近上线"永远显示"1分钟前"）。
	_, err = s.Exec(`UPDATE devices SET name=?, hostname=?, vendor=?, current_ip=?, link_type=?, ssid=?, status=?, notes=?, last_seen=CASE WHEN ?=0 THEN last_seen ELSE ? END WHERE mac=?`,
		name, hostname, vendor, ip, link, ssid, status, notes, ts, ts, d.MAC)
	return err
}

func (s *Store) SetDevice(mac, name, notes string, ignored, fixedServer *bool) error {
	q := "UPDATE devices SET"
	var args []any
	if name != "" {
		q += " name=?,"
		args = append(args, name)
	}
	if notes != "" {
		q += " notes=?,"
		args = append(args, notes)
	}
	if ignored != nil {
		q += " status=?,"
		if *ignored {
			args = append(args, "ignored")
		} else {
			args = append(args, "tracked")
		}
	}
	if fixedServer != nil {
		q += " fixed_server=?,"
		args = append(args, boolToInt(*fixedServer))
	}
	if len(args) == 0 {
		return nil
	}
	q = strings.TrimSuffix(q, ",") + " WHERE mac=?"
	args = append(args, mac)
	_, err := s.Exec(q, args...)
	return err
}

func (s *Store) SaveIPHistory(mac, ip string, ts int64) error {
	_, err := s.Exec(`INSERT INTO device_ip_history (mac, ip, first_seen, last_seen) VALUES (?,?,?,?)
		ON CONFLICT(mac, ip) DO UPDATE SET last_seen=excluded.last_seen`, mac, ip, ts, ts)
	return err
}

type DeviceFilter struct {
	HideFixedServers bool
	Online           *bool
	Status           string
	Q                string
}

func (s *Store) ListDevices(f DeviceFilter) ([]Device, error) {
	q := `SELECT mac, name, hostname, vendor, current_ip, link_type, ssid, status,
		likely_random_mac, first_seen, last_seen, notes, fixed_server FROM devices WHERE 1=1`
	var args []any
	if f.Q != "" {
		q += " AND (mac LIKE ? OR name LIKE ? OR hostname LIKE ? OR current_ip LIKE ?)"
		like := "%" + strings.ToLower(f.Q) + "%"
		args = append(args, like, like, like, like)
	}
	if f.Status != "" {
		q += " AND status=?"
		args = append(args, f.Status)
	}
	if f.HideFixedServers {
		q += " AND fixed_server=0"
	}
	q += " ORDER BY last_seen DESC"
	rows, err := s.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		var d Device
		var random int
		if err := rows.Scan(&d.MAC, &d.Name, &d.Hostname, &d.Vendor, &d.CurrentIP, &d.LinkType, &d.SSID,
			&d.Status, &random, &d.FirstSeen, &d.LastSeen, &d.Notes, &d.FixedServer); err != nil {
			return nil, err
		}
		d.LikelyRandomMAC = random == 1
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeviceView 给 API 用的富视图（含在线状态与订阅渠道）。
type DeviceView struct {
	Device
	Online    bool    `json:"online"`
	Online24h float64 `json:"online_ratio_24h"` // 0..1
	Channels  []int64 `json:"channels"`
}

func (s *Store) ListDeviceViews(f DeviceFilter) ([]DeviceView, error) {
	devs, err := s.ListDevices(f)
	if err != nil {
		return nil, err
	}
	out := make([]DeviceView, 0, len(devs))
	for _, d := range devs {
		online, _ := s.IsOnline(d.MAC)
		ratio, _ := s.OnlineRatio24h(d.MAC)
		chans, _ := s.Subscriptions(d.MAC)
		out = append(out, DeviceView{
			Device:    d,
			Online:    online,
			Online24h: ratio,
			Channels:  chans,
		})
	}
	if f.Online != nil {
		filtered := out[:0]
		for _, v := range out {
			if v.Online == *f.Online {
				filtered = append(filtered, v)
			}
		}
		out = filtered
	}
	return out, nil
}

func (s *Store) IsOnline(mac string) (bool, error) {
	var n int
	err := s.QueryRow(`SELECT count(*) FROM presence_sessions WHERE mac=? AND end_at IS NULL`, mac).Scan(&n)
	return n > 0, err
}

func (s *Store) OnlineRatio24h(mac string) (float64, error) {
	now := nowUnix()
	day := now - 86400
	var totalS int
	rows, err := s.Query(`SELECT start_at, end_at, duration_s FROM presence_sessions
		WHERE mac=? AND start_at >= ?`, mac, day)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var start int64
		var end, dur sql.NullInt64
		if err := rows.Scan(&start, &end, &dur); err != nil {
			return 0, err
		}
		e := end.Int64
		if !end.Valid {
			e = now
		}
		if dur.Valid {
			totalS += int(dur.Int64)
		} else {
			totalS += maxInt(0, int(e-start))
		}
	}
	return clampRatio(float64(totalS) / 86400.0), nil
}

func (s *Store) Subscriptions(mac string) ([]int64, error) {
	rows, err := s.Query(`SELECT channel_id FROM device_channel_subscriptions WHERE mac=?`, mac)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]int64, 0)
	for rows.Next() {
		var c int64
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) SaveSubscriptions(mac string, channelIDs []int64, events []string) error {
	tx, err := s.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM device_channel_subscriptions WHERE mac=?`, mac); err != nil {
		_ = tx.Rollback()
		return err
	}
	ev, _ := json.Marshal(events)
	for _, c := range channelIDs {
		if _, err := tx.Exec(`INSERT INTO device_channel_subscriptions (mac, channel_id, events) VALUES (?,?,?)`,
			mac, c, string(ev)); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// CountOnline 当前在线设备数（存在未关闭区间）。
func (s *Store) CountOnline() (int, error) {
	var n int
	err := s.QueryRow(`SELECT count(DISTINCT mac) FROM presence_sessions WHERE end_at IS NULL`).Scan(&n)
	return n, err
}

// CountNewSince 统计自 since（unix 秒）以来新登记的设备数。
func (s *Store) CountNewSince(since int64) (int, error) {
	var n int
	err := s.QueryRow(`SELECT count(*) FROM devices WHERE first_seen >= ?`, since).Scan(&n)
	return n, err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func clampRatio(r float64) float64 {
	if r < 0 {
		return 0
	}
	if r > 1 {
		return 1
	}
	return r
}
