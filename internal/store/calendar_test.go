package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestCalendarMigrationPreservesHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, name := range []string{"001_init.sql", "002_fixed_servers.sql"} {
		schema, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(schema)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`PRAGMA user_version=2;
		INSERT INTO devices(mac,first_seen,last_seen) VALUES ('AA-BB-CC-DD-EE-FF',100,300);
		INSERT INTO presence_sessions(mac,start_at,end_at) VALUES ('AA-BB-CC-DD-EE-FF',100,200),('AA-BB-CC-DD-EE-FF',300,NULL)`); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		st, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		spans, err := st.CalendarSessions("AA-BB-CC-DD-EE-FF", 0, 400)
		st.Close()
		if err != nil || len(spans) != 2 || spans[0].Start != 100 || spans[0].End != 200 || spans[1].End != 400 {
			t.Fatalf("migration/reopen lost history: %+v, %v", spans, err)
		}
	}
}

func TestCalendarCoverageExcludesFailedAndMissingRounds(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "calendar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, round := range []struct {
		at int64
		ok bool
	}{{100, true}, {160, false}, {220, true}, {1000, true}} {
		if _, err := st.InsertProbeRound(round.at, round.ok, false, ""); err != nil {
			t.Fatal(err)
		}
	}
	spans, err := st.CalendarCoverage(100, 1100, 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 3 || spans[0].End != 160 || spans[1].Start != 220 || spans[1].End != 340 || spans[2].Start != 1000 {
		t.Fatalf("bad coverage: %+v", spans)
	}
}

func TestCalendarSessionsPreserveShortGaps(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "calendar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	const mac = "AA-BB-CC-DD-EE-FF"
	if err := st.UpsertDevice(Device{MAC: mac}); err != nil {
		t.Fatal(err)
	}
	for _, at := range []int64{100, 120} {
		if err := st.OpenCalendarSession(mac, at); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.CloseCalendarSession(mac, 200); err != nil {
		t.Fatal(err)
	}
	if err := st.OpenCalendarSession(mac, 250); err != nil {
		t.Fatal(err)
	}
	spans, err := st.CalendarSessions(mac, 0, 400)
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 2 || spans[0].Start != 100 || spans[0].End != 200 || spans[1].Start != 250 || spans[1].End != 400 {
		t.Fatalf("lost boundaries: %+v", spans)
	}
}
