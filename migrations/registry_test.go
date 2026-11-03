package migrations

import (
	"strings"
	"testing"
)

func TestEmbeddedMigrationPairs(t *testing.T) {
	steps, err := All()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if len(steps) != 4 {
		t.Fatalf("migration count = %d, want 4", len(steps))
	}
	for index, step := range steps {
		if step.Version != int64(index)+1 {
			t.Errorf("unexpected migration version %d", step.Version)
		}
		if len(step.Checksum) != 64 {
			t.Errorf("migration %d missing SHA-256 checksum", step.Version)
		}
		if !strings.Contains(step.UpSQL, "CREATE TABLE") && !strings.Contains(step.UpSQL, "ALTER TABLE") &&
			!strings.Contains(step.UpSQL, "CREATE INDEX") {
			t.Errorf("migration %d has no schema DDL", step.Version)
		}
	}
}

func TestHasSQLStatement(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"empty", "", false},
		{"comments only", "-- edit this file\n -- another comment", false},
		{"create table", "-- migration\nCREATE TABLE thing(id bigint);", true},
		{"delete statement", "DELETE FROM items WHERE false;", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := hasSQLStatement(test.content); got != test.want {
				t.Fatalf("hasSQLStatement(%q) = %v; want %v", test.content, got, test.want)
			}
		})
	}
}

func TestCheckHistory(t *testing.T) {
	steps, err := All()
	if err != nil {
		t.Fatalf("load migration list: %v", err)
	}
	tests := []struct {
		name      string
		applied   []Applied
		wantError bool
	}{
		{"empty history", nil, false},
		{"valid first", []Applied{{Version: 1, Name: steps[0].Name, Checksum: steps[0].Checksum}}, false},
		{"unknown version", []Applied{{Version: 5}}, true},
		{"modified SQL", []Applied{{Version: 1, Checksum: "bad"}}, true},
		{"renamed migration", []Applied{{Version: 1, Name: "other"}}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := checkHistory(steps, test.applied)
			if (err != nil) != test.wantError {
				t.Fatalf("checkHistory() error = %v; wantError=%v", err, test.wantError)
			}
		})
	}
}

func TestSessionExpiryMigration(t *testing.T) {
	steps, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(steps[2].UpSQL, "ON sessions(expires_at)") {
		t.Fatalf("migration 003 does not index session expiration: %s", steps[2].UpSQL)
	}
}
