package store

import (
	"database/sql"
	"time"
)

func (s *Store) UpsertSourceState(id, kind, label string) error {
	_, err := s.Exec(`
		INSERT INTO source_state (id, kind, healthy, consecutive_fail, last_error, locked_until, host_key, label)
		VALUES (?, ?, 1, 0, '', 0, '', ?)
		ON CONFLICT(id) DO UPDATE SET kind=excluded.kind, label=excluded.label`,
		id, kind, label)
	return err
}

func (s *Store) GetSourceState(id string) (*SourceStateRow, error) {
	var r SourceStateRow
	var ok int
	var lka, lf, lu sql.NullInt64
	var le, hk sql.NullString
	err := s.QueryRow(`SELECT id, kind, healthy, last_ok_at, consecutive_fail, last_error, locked_until, host_key, label
		FROM source_state WHERE id=?`, id).
		Scan(&r.ID, &r.Kind, &ok, &lka, &lf, &le, &lu, &hk, &r.Label)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.Healthy = ok == 1
	r.LastOKAt, r.ConsecutiveFail = lka.Int64, int(lf.Int64)
	r.LastError, r.HostKey = le.String, hk.String
	r.LockedUntil = lu.Int64
	return &r, nil
}

func (s *Store) MarkSourceOK(id string) error {
	_, err := s.Exec(`UPDATE source_state SET healthy=1, consecutive_fail=0, last_error='', last_ok_at=? WHERE id=?`,
		time.Now().Unix(), id)
	return err
}

func (s *Store) MarkSourceFail(id, errMsg string) error {
	_, err := s.Exec(`UPDATE source_state SET healthy=0, consecutive_fail=consecutive_fail+1, last_error=? WHERE id=?`,
		errMsg, id)
	return err
}

func (s *Store) SetSourceLocked(id string, until int64) error {
	_, err := s.Exec(`UPDATE source_state SET locked_until=?, last_error='locked', consecutive_fail=consecutive_fail+1 WHERE id=?`,
		until, id)
	return err
}

func (s *Store) SetSourceHostKey(id, host, fingerprint string) error {
	_, err := s.Exec(`INSERT INTO source_host_keys (source_id, host, fingerprint) VALUES (?, ?, ?)
		ON CONFLICT(source_id, host) DO UPDATE SET fingerprint=excluded.fingerprint`, id, host, fingerprint)
	return err
}

func (s *Store) GetSourceHostKeys(id string) (map[string]string, error) {
	rows, err := s.Query(`SELECT host, fingerprint FROM source_host_keys WHERE source_id=?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var host, fp string
		if err := rows.Scan(&host, &fp); err != nil {
			return nil, err
		}
		out[host] = fp
	}
	return out, rows.Err()
}

// ListSourceStates 返回所有采集源健康状态。
func (s *Store) ListSourceStates() ([]SourceStateRow, error) {
	rows, err := s.Query(`SELECT id, kind, healthy, last_ok_at, consecutive_fail, last_error, locked_until, host_key, label FROM source_state ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SourceStateRow
	for rows.Next() {
		var r SourceStateRow
		var ok int
		var lka, lf, lu sql.NullInt64
		var le, hk sql.NullString
		if err := rows.Scan(&r.ID, &r.Kind, &ok, &lka, &lf, &le, &lu, &hk, &r.Label); err != nil {
			return nil, err
		}
		r.Healthy = ok == 1
		r.LastOKAt, r.ConsecutiveFail = lka.Int64, int(lf.Int64)
		r.LastError, r.HostKey = le.String, hk.String
		r.LockedUntil = lu.Int64
		out = append(out, r)
	}
	return out, rows.Err()
}
