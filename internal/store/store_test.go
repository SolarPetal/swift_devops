package store

import "testing"

func TestNormalizeDriver(t *testing.T) {
	tests := map[string]string{
		"":           DriverSQLite,
		"sqlite":     DriverSQLite,
		"sqlite3":    DriverSQLite,
		"MYSQL":      DriverMySQL,
		"postgres":   DriverPostgres,
		"postgresql": DriverPostgres,
		"pg":         DriverPostgres,
	}
	for in, want := range tests {
		if got := NormalizeDriver(in); got != want {
			t.Fatalf("NormalizeDriver(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSQLiteDSNAddsPragmas(t *testing.T) {
	got := sqliteDSN("./data/app.db")
	want := "./data/app.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	if got != want {
		t.Fatalf("sqliteDSN without query = %q, want %q", got, want)
	}

	got = sqliteDSN("file:app.db?cache=shared")
	want = "file:app.db?cache=shared&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	if got != want {
		t.Fatalf("sqliteDSN with query = %q, want %q", got, want)
	}
}

func TestOpenRejectsUnsupportedDriverBeforeDial(t *testing.T) {
	if _, err := Open("oracle", "example"); err == nil {
		t.Fatal("expected unsupported driver error")
	}
}

func TestAutoMigrateSQLite(t *testing.T) {
	db, err := Open(DriverSQLite, ":memory:")
	if err != nil {
		t.Fatalf("open sqlite memory db: %v", err)
	}
	if err := AutoMigrate(db, DriverSQLite); err != nil {
		t.Fatalf("AutoMigrate sqlite: %v", err)
	}
	if !db.Migrator().HasTable("hosts") {
		t.Fatal("expected hosts table after AutoMigrate")
	}
}
