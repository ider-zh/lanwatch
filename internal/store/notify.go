package store

import (
	"database/sql"
	"time"
)

// ---- channels ----

func (s *Store) CreateChannel(typ, name string, configEnc []byte, enabled bool) (int64, error) {
	res, err := s.Exec(`INSERT INTO channels (type, name, config_enc, enabled, created_at) VALUES (?,?,?,?,?)`,
		typ, name, configEnc, boolToInt(enabled), time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetChannel(id int64) (*Channel, error) {
	var c Channel
	var enabled int
	err := s.QueryRow(`SELECT id, type, name, config_enc, enabled, created_at FROM channels WHERE id=?`, id).
		Scan(&c.ID, &c.Type, &c.Name, &c.ConfigEnc, &enabled, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	c.Enabled = enabled == 1
	return &c, nil
}

func (s *Store) ListChannels() ([]Channel, error) {
	rows, err := s.Query(`SELECT id, type, name, config_enc, enabled, created_at FROM channels ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Channel
	for rows.Next() {
		var c Channel
		var enabled int
		if err := rows.Scan(&c.ID, &c.Type, &c.Name, &c.ConfigEnc, &enabled, &c.CreatedAt); err != nil {
			return nil, err
		}
		c.Enabled = enabled == 1
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) UpdateChannel(id int64, name string, configEnc []byte, enabled bool) error {
	_, err := s.Exec(`UPDATE channels SET name=?, config_enc=?, enabled=? WHERE id=?`,
		name, configEnc, boolToInt(enabled), id)
	return err
}

func (s *Store) SetChannelEnabled(id int64, enabled bool) error {
	_, err := s.Exec(`UPDATE channels SET enabled=? WHERE id=?`, boolToInt(enabled), id)
	return err
}

func (s *Store) DeleteChannel(id int64) error {
	// 级联清理订阅/规则关联
	if _, err := s.Exec(`DELETE FROM device_channel_subscriptions WHERE channel_id=?`, id); err != nil {
		return err
	}
	if _, err := s.Exec(`DELETE FROM rule_channels WHERE channel_id=?`, id); err != nil {
		return err
	}
	_, err := s.Exec(`DELETE FROM channels WHERE id=?`, id)
	return err
}

// ---- rules ----

func (s *Store) CreateRule(name string, enabled bool, scope, scopeRef, events, quietHours string, minOffline, cooldown, digest int) (int64, error) {
	res, err := s.Exec(`INSERT INTO notify_rules
		(name, enabled, scope, scope_ref, events, min_offline_sec, quiet_hours, cooldown_sec, digest_sec, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		name, boolToInt(enabled), scope, scopeRef, events, minOffline, quietHours, cooldown, digest, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetRule(id int64) (*NotifyRule, error) {
	var r NotifyRule
	var enabled int
	err := s.QueryRow(`SELECT id, name, enabled, scope, scope_ref, events, min_offline_sec, quiet_hours, cooldown_sec, digest_sec, created_at
		FROM notify_rules WHERE id=?`, id).
		Scan(&r.ID, &r.Name, &enabled, &r.Scope, &r.ScopeRef, &r.Events, &r.MinOfflineSec, &r.QuietHours, &r.CooldownSec, &r.DigestSec, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	r.Enabled = enabled == 1
	return &r, nil
}

func (s *Store) ListRules() ([]NotifyRule, error) {
	rows, err := s.Query(`SELECT id, name, enabled, scope, scope_ref, events, min_offline_sec, quiet_hours, cooldown_sec, digest_sec, created_at
		FROM notify_rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NotifyRule
	for rows.Next() {
		var r NotifyRule
		var enabled int
		if err := rows.Scan(&r.ID, &r.Name, &enabled, &r.Scope, &r.ScopeRef, &r.Events, &r.MinOfflineSec, &r.QuietHours, &r.CooldownSec, &r.DigestSec, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Enabled = enabled == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) UpdateRule(id int64, name string, enabled bool, scope, scopeRef, events, quietHours string, minOffline, cooldown, digest int) error {
	_, err := s.Exec(`UPDATE notify_rules SET
		name=?, enabled=?, scope=?, scope_ref=?, events=?, min_offline_sec=?, quiet_hours=?, cooldown_sec=?, digest_sec=?
		WHERE id=?`,
		name, boolToInt(enabled), scope, scopeRef, events, minOffline, quietHours, cooldown, digest, id)
	return err
}

func (s *Store) DeleteRule(id int64) error {
	if _, err := s.Exec(`DELETE FROM rule_channels WHERE rule_id=?`, id); err != nil {
		return err
	}
	_, err := s.Exec(`DELETE FROM notify_rules WHERE id=?`, id)
	return err
}

func (s *Store) SetRuleChannels(ruleID int64, channelIDs []int64) error {
	if _, err := s.Exec(`DELETE FROM rule_channels WHERE rule_id=?`, ruleID); err != nil {
		return err
	}
	for _, cid := range channelIDs {
		if _, err := s.Exec(`INSERT OR IGNORE INTO rule_channels (rule_id, channel_id) VALUES (?,?)`, ruleID, cid); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ListRuleChannelIDs(ruleID int64) ([]int64, error) {
	rows, err := s.Query(`SELECT channel_id FROM rule_channels WHERE rule_id=?`, ruleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]int64, 0)
	for rows.Next() {
		var cid int64
		if err := rows.Scan(&cid); err != nil {
			return nil, err
		}
		out = append(out, cid)
	}
	return out, rows.Err()
}

// ---- device subscriptions ----

// SetSubscription 设置某设备在某渠道上订阅的事件（events 为 JSON 数组字符串）。
// 传空 events 表示取消该设备的该渠道订阅。
func (s *Store) SetSubscription(mac string, channelID int64, events string) error {
	if events == "" || events == "[]" {
		_, err := s.Exec(`DELETE FROM device_channel_subscriptions WHERE mac=? AND channel_id=?`, mac, channelID)
		return err
	}
	_, err := s.Exec(`INSERT INTO device_channel_subscriptions (mac, channel_id, events) VALUES (?,?,?)
		ON CONFLICT(mac, channel_id) DO UPDATE SET events=excluded.events`, mac, channelID, events)
	return err
}

// ListSubscriptions 返回某设备的全部订阅。
func (s *Store) ListSubscriptions(mac string) ([]Subscription, error) {
	rows, err := s.Query(`SELECT mac, channel_id, events FROM device_channel_subscriptions WHERE mac=?`, mac)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Subscription, 0)
	for rows.Next() {
		var sub Subscription
		if err := rows.Scan(&sub.MAC, &sub.ChannelID, &sub.Events); err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

// ClearSubscriptions 删除某设备的全部渠道订阅（用于整体替换）。
func (s *Store) ClearSubscriptions(mac string) error {
	_, err := s.Exec(`DELETE FROM device_channel_subscriptions WHERE mac=?`, mac)
	return err
}

// ListChannelsForDevice 返回订阅了该设备的渠道 id 列表（去重）。
func (s *Store) ListChannelsForDevice(mac string) ([]int64, error) {
	rows, err := s.Query(`SELECT DISTINCT channel_id FROM device_channel_subscriptions WHERE mac=?`, mac)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]int64, 0)
	for rows.Next() {
		var cid int64
		if err := rows.Scan(&cid); err != nil {
			return nil, err
		}
		out = append(out, cid)
	}
	return out, rows.Err()
}

// ---- notify logs ----

// InsertNotifyLog 写入一条发送日志，返回自增 id。
func (s *Store) InsertNotifyLog(ruleID *int64, channelID int64, mac *string, event, status string, payload string) (int64, error) {
	res, err := s.Exec(`INSERT INTO notify_logs
		(rule_id, channel_id, mac, event, status, attempts, payload, created_at)
		VALUES (?,?,?,?,?,0,?,?)`,
		ruleID, channelID, mac, event, status, payload, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateNotifyLog(id int64, status string, attempts int, sendErr string, sentAt *int64) error {
	_, err := s.Exec(`UPDATE notify_logs SET status=?, attempts=?, error=?, sent_at=? WHERE id=?`,
		status, attempts, sendErr, sentAt, id)
	return err
}

func (s *Store) GetNotifyLog(id int64) (*NotifyLog, error) {
	var l NotifyLog
	var ruleID, sentAt sql.NullInt64
	var mac, sendErr sql.NullString
	err := s.QueryRow(`SELECT id, rule_id, channel_id, mac, event, status, attempts, error, payload, created_at, sent_at
		FROM notify_logs WHERE id=?`, id).
		Scan(&l.ID, &ruleID, &l.ChannelID, &mac, &l.Event, &l.Status, &l.Attempts, &sendErr, &l.Payload, &l.CreatedAt, &sentAt)
	if err != nil {
		return nil, err
	}
	if ruleID.Valid {
		v := ruleID.Int64
		l.RuleID = &v
	}
	if mac.Valid {
		v := mac.String
		l.MAC = &v
	}
	if sentAt.Valid {
		v := sentAt.Int64
		l.SentAt = &v
	}
	l.Error = sendErr.String
	return &l, nil
}

// NotifyLogRow 列表视图行（含渠道名称与设备名称）。
type NotifyLogRow struct {
	NotifyLog
	ChannelName    string
	ChannelType    string
	DeviceName     string // 用户在「设备」页设置的备注名，可能为空
	DeviceHostname string // 采集到的主机名，可能为空
}

// DeviceLabel 用于列表展示：优先备注名，其次主机名，都没有则回落 MAC。
func (r NotifyLogRow) DeviceLabel() string {
	if r.DeviceName != "" {
		return r.DeviceName
	}
	if r.DeviceHostname != "" {
		return r.DeviceHostname
	}
	if r.MAC != nil {
		return *r.MAC
	}
	return ""
}

// ListNotifyLogs 分页/筛选查询发送日志（按时间倒序）。
func (s *Store) ListNotifyLogs(limit, offset int, onlyErrors bool) ([]NotifyLogRow, error) {
	// 左连接 devices 取备注名/主机名：通知日志只存 mac，不关联就只剩一串 MAC，无法辨认是哪台设备
	q := `SELECT n.id, n.rule_id, n.channel_id, n.mac, n.event, n.status, n.attempts, n.error, n.payload, n.created_at, n.sent_at, c.name, c.type,
		COALESCE(d.name, ''), COALESCE(d.hostname, '')
		FROM notify_logs n
		LEFT JOIN channels c ON c.id=n.channel_id
		LEFT JOIN devices d ON d.mac=n.mac WHERE 1=1`
	args := []interface{}{}
	if onlyErrors {
		q += ` AND n.status='failed'`
	}
	q += ` ORDER BY n.created_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := s.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NotifyLogRow
	for rows.Next() {
		var l NotifyLog
		var ruleID, sentAt sql.NullInt64
		var mac, sendErr, chName, chType, devName, devHost sql.NullString
		if err := rows.Scan(&l.ID, &ruleID, &l.ChannelID, &mac, &l.Event, &l.Status, &l.Attempts, &sendErr, &l.Payload, &l.CreatedAt, &sentAt, &chName, &chType, &devName, &devHost); err != nil {
			return nil, err
		}
		if ruleID.Valid {
			v := ruleID.Int64
			l.RuleID = &v
		}
		if mac.Valid {
			v := mac.String
			l.MAC = &v
		}
		if sentAt.Valid {
			v := sentAt.Int64
			l.SentAt = &v
		}
		l.Error = sendErr.String
		out = append(out, NotifyLogRow{
			NotifyLog:      l,
			ChannelName:    chName.String,
			ChannelType:    chType.String,
			DeviceName:     devName.String,
			DeviceHostname: devHost.String,
		})
	}
	return out, rows.Err()
}
