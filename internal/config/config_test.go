package config

import "testing"

func TestNormalizeDatabaseDriver(t *testing.T) {
	tests := map[string]string{
		"":           "sqlite",
		"sqlite3":    "sqlite",
		"MYSQL":      "mysql",
		"postgresql": "postgres",
		"pg":         "postgres",
	}
	for in, want := range tests {
		if got := normalizeDatabaseDriver(in); got != want {
			t.Fatalf("normalizeDatabaseDriver(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateRejectsUnsupportedDatabaseDriver(t *testing.T) {
	c := &Config{}
	c.applyDefaults()
	c.Database.Driver = "oracle"
	c.Database.DSN = "example"
	c.Security.MasterKey = "key"
	c.Security.JWTSecret = "secret"
	c.Admin.Username = "admin"
	c.Admin.PasswordBcrypt = "hash"
	c.Storage.ArtifactDir = "./data/artifacts"
	if err := c.validate(); err == nil {
		t.Fatal("expected unsupported database driver error")
	}
}
