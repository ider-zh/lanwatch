package store

import (
	"database/sql"
	"time"
)

// CreateUser 创建用户（密码需调用方先用 bcrypt 哈希）。
func (s *Store) CreateUser(username, passwordHash, role string) (int64, error) {
	res, err := s.Exec(`INSERT INTO users (username, password_hash, role, created_at, last_login_at) VALUES (?,?,?,?,NULL)`,
		username, passwordHash, role, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetUserByUsername(username string) (*User, error) {
	var u User
	var ll sql.NullInt64
	err := s.QueryRow(`SELECT id, username, password_hash, role, created_at, last_login_at
		FROM users WHERE username=?`, username).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt, &ll)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if ll.Valid {
		v := ll.Int64
		u.LastLoginAt = &v
	}
	return &u, nil
}

func (s *Store) GetUserByID(id int64) (*User, error) {
	var u User
	var ll sql.NullInt64
	err := s.QueryRow(`SELECT id, username, password_hash, role, created_at, last_login_at
		FROM users WHERE id=?`, id).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt, &ll)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if ll.Valid {
		v := ll.Int64
		u.LastLoginAt = &v
	}
	return &u, nil
}

func (s *Store) CountUsers() (int, error) {
	var n int
	err := s.QueryRow(`SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) SetLastLogin(userID int64, ts int64) error {
	_, err := s.Exec(`UPDATE users SET last_login_at=? WHERE id=?`, ts, userID)
	return err
}

// SaveSession 写入会话（token 调用方应已做 SHA256）。
func (s *Store) SaveSession(token string, userID, createdAt, expiresAt, lastSeen int64) error {
	_, err := s.Exec(`INSERT INTO sessions (token, user_id, created_at, expires_at, last_seen_at) VALUES (?,?,?,?,?)`,
		token, userID, createdAt, expiresAt, lastSeen)
	return err
}

func (s *Store) GetSessionUser(token string) (*User, error) {
	var userID, expires, lastSeen int64
	err := s.QueryRow(`SELECT user_id, expires_at, last_seen_at FROM sessions WHERE token=?`, token).
		Scan(&userID, &expires, &lastSeen)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if time.Now().Unix() > expires {
		_ = s.DeleteSession(token)
		return nil, nil
	}
	return s.GetUserByID(userID)
}

func (s *Store) TouchSession(token string, ts int64) error {
	_, err := s.Exec(`UPDATE sessions SET last_seen_at=? WHERE token=?`, ts, token)
	return err
}

func (s *Store) DeleteSession(token string) error {
	_, err := s.Exec(`DELETE FROM sessions WHERE token=?`, token)
	return err
}

// PruneSessions 删除过期会话。
func (s *Store) PruneExpiredSessions(now int64) (int64, error) {
	res, err := s.Exec(`DELETE FROM sessions WHERE expires_at < ?`, now)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
