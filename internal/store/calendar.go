package store

func (s *Store) OpenCalendarSession(mac string, at int64) error {
	_, err := s.Exec(`INSERT INTO calendar_sessions(mac, start_at)
		SELECT ?, ? WHERE NOT EXISTS (SELECT 1 FROM calendar_sessions WHERE mac=? AND end_at IS NULL)`, mac, at, mac)
	return err
}

func (s *Store) CloseCalendarSession(mac string, at int64) error {
	_, err := s.Exec(`UPDATE calendar_sessions SET end_at=? WHERE mac=? AND end_at IS NULL`, at, mac)
	return err
}

type CalendarSpan struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

func (s *Store) CalendarSessions(mac string, from, to int64) ([]CalendarSpan, error) {
	rows, err := s.Query(`SELECT start_at, COALESCE(end_at, ?) FROM calendar_sessions
		WHERE mac=? AND start_at<? AND (end_at IS NULL OR end_at>?) ORDER BY start_at`, to, mac, to, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	spans := make([]CalendarSpan, 0)
	for rows.Next() {
		var span CalendarSpan
		if err := rows.Scan(&span.Start, &span.End); err != nil {
			return nil, err
		}
		spans = append(spans, span)
	}
	return spans, rows.Err()
}

func (s *Store) CalendarCoverage(from, to, interval int64) ([]CalendarSpan, error) {
	rows, err := s.Query(`SELECT COALESCE(finished_at, started_at), ok FROM probe_rounds
		WHERE started_at>=? AND started_at<? AND skipped=0 ORDER BY COALESCE(finished_at, started_at)`, from-interval*2, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	spans := make([]CalendarSpan, 0)
	var previous int64
	var healthy bool
	for rows.Next() {
		var at int64
		var ok bool
		if err := rows.Scan(&at, &ok); err != nil {
			return nil, err
		}
		if healthy && at > previous {
			spans = append(spans, CalendarSpan{Start: previous, End: min(at, previous+interval*2, to)})
		}
		previous, healthy = at, ok
	}
	if healthy && to > previous {
		spans = append(spans, CalendarSpan{Start: previous, End: min(to, previous+interval*2)})
	}
	return spans, rows.Err()
}
