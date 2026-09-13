package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestFixedServerMigrationAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := migrationsFS.ReadFile("migrations/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 1"); err != nil {
		t.Fatal(err)
	}
	const mac = "AA:00:00:00:00:01"
	if _, err := db.Exec("INSERT INTO devices (mac, current_ip, first_seen, last_seen) VALUES (?, '192.168.1.1', 1, 1)", mac); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := st.GetDevice(mac)
	if err != nil {
		t.Fatal(err)
	}
	if d == nil || d.FixedServer {
		t.Fatalf("old device migrated incorrectly: %+v", d)
	}
	mark := true
	if err := st.SetDevice(mac, "", "", nil, &mark); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	d, err = st.GetDevice(mac)
	if err != nil {
		t.Fatal(err)
	}
	if d == nil || !d.FixedServer {
		t.Fatalf("mark lost after reopen: %+v", d)
	}
}
