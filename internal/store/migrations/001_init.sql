-- lanwatch schema (001)
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS devices (
  mac            TEXT PRIMARY KEY,
  name           TEXT NOT NULL DEFAULT '',
  hostname       TEXT NOT NULL DEFAULT '',
  vendor         TEXT NOT NULL DEFAULT '',
  current_ip     TEXT,
  link_type      TEXT NOT NULL DEFAULT 'unknown',
  ssid           TEXT NOT NULL DEFAULT '',
  status         TEXT NOT NULL DEFAULT 'new',
  likely_random_mac INTEGER NOT NULL DEFAULT 0,
  first_seen     INTEGER NOT NULL,
  last_seen      INTEGER NOT NULL,
  notes          TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS presence_sessions (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  mac         TEXT NOT NULL REFERENCES devices(mac) ON DELETE CASCADE,
  start_at    INTEGER NOT NULL,
  end_at      INTEGER,
  duration_s  INTEGER,
  end_reason  TEXT
);
CREATE INDEX IF NOT EXISTS idx_sessions_mac_time ON presence_sessions(mac, start_at);

CREATE TABLE IF NOT EXISTS probe_samples (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  round_id   INTEGER NOT NULL,
  mac        TEXT NOT NULL,
  ip         TEXT,
  present    INTEGER NOT NULL,
  confidence INTEGER NOT NULL,
  source_id  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_samples_round ON probe_samples(round_id);

CREATE TABLE IF NOT EXISTS probe_rounds (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  started_at  INTEGER NOT NULL,
  finished_at INTEGER,
  ok          INTEGER NOT NULL DEFAULT 1,
  skipped     INTEGER NOT NULL DEFAULT 0,
  error       TEXT
);

CREATE TABLE IF NOT EXISTS device_ip_history (
  mac TEXT NOT NULL,
  ip TEXT NOT NULL,
  first_seen INTEGER NOT NULL,
  last_seen INTEGER NOT NULL,
  PRIMARY KEY (mac, ip)
);

CREATE TABLE IF NOT EXISTS channels (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  type TEXT NOT NULL,
  name TEXT NOT NULL,
  config_enc BLOB NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS notify_rules (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  scope TEXT NOT NULL,
  scope_ref TEXT NOT NULL DEFAULT '',
  events TEXT NOT NULL,
  min_offline_sec INTEGER NOT NULL DEFAULT 120,
  quiet_hours TEXT NOT NULL DEFAULT '[]',
  cooldown_sec INTEGER NOT NULL DEFAULT 300,
  digest_sec INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS rule_channels (
  rule_id INTEGER NOT NULL,
  channel_id INTEGER NOT NULL,
  PRIMARY KEY (rule_id, channel_id)
);

CREATE TABLE IF NOT EXISTS device_channel_subscriptions (
  mac TEXT NOT NULL,
  channel_id INTEGER NOT NULL,
  events TEXT NOT NULL,
  PRIMARY KEY (mac, channel_id)
);

CREATE TABLE IF NOT EXISTS notify_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  rule_id INTEGER,
  channel_id INTEGER NOT NULL,
  mac TEXT,
  event TEXT NOT NULL,
  status TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  error TEXT,
  payload TEXT,
  created_at INTEGER NOT NULL,
  sent_at INTEGER
);
CREATE INDEX IF NOT EXISTS idx_nlog_time ON notify_logs(created_at);

CREATE TABLE IF NOT EXISTS source_state (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  healthy INTEGER NOT NULL DEFAULT 1,
  last_ok_at INTEGER,
  consecutive_fail INTEGER NOT NULL DEFAULT 0,
  last_error TEXT,
  locked_until INTEGER NOT NULL DEFAULT 0,
  host_key TEXT,
  label TEXT
);

CREATE TABLE IF NOT EXISTS source_host_keys (
  source_id TEXT NOT NULL,
  host TEXT NOT NULL,
  fingerprint TEXT NOT NULL,
  PRIMARY KEY (source_id, host)
);

CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL DEFAULT 'admin',
  created_at INTEGER,
  last_login_at INTEGER
);

CREATE TABLE IF NOT EXISTS sessions (
  token TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  last_seen_at INTEGER
);

CREATE TABLE IF NOT EXISTS device_daily_stats (
  mac TEXT NOT NULL,
  day TEXT NOT NULL,
  online_s INTEGER NOT NULL,
  sessions INTEGER NOT NULL,
  flaps INTEGER NOT NULL,
  PRIMARY KEY (mac, day)
);
