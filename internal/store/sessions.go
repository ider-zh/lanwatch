package store

import (
	"database/sql"
	"time"
)

// InsertProbeRound 记录一次采集轮次，返回自增 id。
func (s *Store) InsertProbeRound(startedAt int64, ok bool, skipped bool, errMsg string) (int64, error) {
	res, err := s.Exec(`INSERT INTO probe_rounds (started_at, ok, skipped, error) VALUES (?,?,?,?)`,
		startedAt, boolToInt(ok), boolToInt(skipped), errMsg)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) FinishProbeRound(id int64, finishedAt int64, ok bool, errMsg string) error {
	_, err := s.Exec(`UPDATE probe_rounds SET finished_at=?, ok=?, error=? WHERE id=?`,
		finishedAt, boolToInt(ok), errMsg, id)
	return err
}

// InsertProbeSample 记录单个观测样本（一台设备在某源某轮中的在线证据）。
func (s *Store) InsertProbeSample(roundID int64, mac, ip string, present bool, confidence int, sourceID string) error {
	_, err := s.Exec(`INSERT INTO probe_samples (round_id, mac, ip, present, confidence, source_id) VALUES (?,?,?,?,?,?)`,
		roundID, mac, ip, boolToInt(present), confidence, sourceID)
	return err
}

// OpenSession 开启一个在线区间。若已存在未关闭区间则先关闭（防御性）。
func (s *Store) OpenSession(mac string, startAt int64, reason string) (int64, error) {
	if err := s.CloseOpenSessions(mac, startAt, "reopen"); err != nil {
		return 0, err
	}
	res, err := s.Exec(`INSERT INTO presence_sessions (mac, start_at, end_at, duration_s, end_reason) VALUES (?,?,NULL,NULL,?)`,
		mac, startAt, reason)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// CloseOpenSessions 关闭该 mac 所有未结束区间，计算时长与结束原因。
func (s *Store) CloseOpenSessions(mac string, endAt int64, reason string) error {
	_, err := s.Exec(`UPDATE presence_sessions
		SET end_at=?, duration_s=(? - start_at), end_reason=?
		WHERE mac=? AND end_at IS NULL`, endAt, endAt, reason, mac)
	return err
}

// LatestSession 返回该 mac 最近一条区间（无论是否已关闭），用于 merge-gap 判断与重开。
func (s *Store) LatestSession(mac string) (*PresenceSession, error) {
	var p PresenceSession
	var endAt, dur sql.NullInt64
	err := s.QueryRow(`SELECT id, mac, start_at, end_at, duration_s, COALESCE(end_reason, '') AS end_reason
		FROM presence_sessions WHERE mac=? ORDER BY id DESC LIMIT 1`, mac).
		Scan(&p.ID, &p.MAC, &p.StartAt, &endAt, &dur, &p.EndReason)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if endAt.Valid {
		v := endAt.Int64
		p.EndAt = &v
	}
	if dur.Valid {
		v := dur.Int64
		p.DurationS = &v
	}
	return &p, nil
}

// CountOpenSessions 返回该 mac 当前未关闭（在线）区间数。
func (s *Store) CountOpenSessions(mac string) (int, error) {
	var n int
	err := s.QueryRow(`SELECT COUNT(*) FROM presence_sessions WHERE mac=? AND end_at IS NULL`, mac).Scan(&n)
	return n, err
}

// ReopenLatestSession 把最近一条已关闭区间重新打开（merge-gap 合并场景）。
func (s *Store) ReopenLatestSession(mac string) error {
	_, err := s.Exec(`UPDATE presence_sessions SET end_at=NULL, duration_s=NULL, end_reason=NULL
		WHERE mac=? AND id=(SELECT MAX(id) FROM presence_sessions WHERE mac=?)`, mac, mac)
	return err
}

// ListSessions 返回区间 [from,to] 内（含边界相交）的在线区间，用于时间轴。
func (s *Store) ListSessions(mac string, from, to int64) ([]PresenceSession, error) {
	rows, err := s.Query(`SELECT id, mac, start_at, end_at, duration_s, COALESCE(end_reason, '') AS end_reason
		FROM presence_sessions
		WHERE mac=? AND start_at < ? AND (end_at IS NULL OR end_at > ?)
		ORDER BY start_at ASC`, mac, to, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PresenceSession
	for rows.Next() {
		var p PresenceSession
		var endAt, dur sql.NullInt64
		if err := rows.Scan(&p.ID, &p.MAC, &p.StartAt, &endAt, &dur, &p.EndReason); err != nil {
			return nil, err
		}
		if endAt.Valid {
			v := endAt.Int64
			p.EndAt = &v
		}
		if dur.Valid {
			v := dur.Int64
			p.DurationS = &v
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PruneSessions 删除早于 before 的已关闭区间。
func (s *Store) PruneSessions(before int64) (int64, error) {
	res, err := s.Exec(`DELETE FROM presence_sessions WHERE end_at IS NOT NULL AND start_at < ?`, before)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// PruneProbeSamples 删除早于 before 的采样样本。
func (s *Store) PruneProbeSamples(before int64) (int64, error) {
	res, err := s.Exec(`DELETE FROM probe_samples WHERE round_id IN (
		SELECT id FROM probe_rounds WHERE started_at < ?)`, before)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// LastRoundAt 返回最近一次成功轮次的时间戳，用于 source 不健康后恢复判定。
func (s *Store) LastRoundAt() (int64, error) {
	var t sql.NullInt64
	err := s.QueryRow(`SELECT MAX(finished_at) FROM probe_rounds WHERE ok=1 AND skipped=0`).Scan(&t)
	if err != nil {
		return 0, err
	}
	if !t.Valid {
		return 0, nil
	}
	return t.Int64, nil
}

func probeNow() int64 { return time.Now().Unix() }
