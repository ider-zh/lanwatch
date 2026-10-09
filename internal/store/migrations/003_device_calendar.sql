ALTER TABLE devices ADD COLUMN watched INTEGER NOT NULL DEFAULT 0;
CREATE TABLE calendar_sessions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  mac TEXT NOT NULL REFERENCES devices(mac),
  start_at INTEGER NOT NULL,
  end_at INTEGER
);
CREATE INDEX idx_calendar_sessions_mac_start ON calendar_sessions(mac, start_at);
INSERT INTO calendar_sessions(mac, start_at, end_at)
SELECT mac, start_at, end_at FROM presence_sessions;
CREATE TABLE calendar_metadata (raw_since INTEGER NOT NULL);
INSERT INTO calendar_metadata(raw_since) VALUES (CAST(strftime('%s', 'now') AS INTEGER));
CREATE INDEX IF NOT EXISTS idx_probe_rounds_started ON probe_rounds(started_at);
