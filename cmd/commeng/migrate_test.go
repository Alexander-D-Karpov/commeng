package main

import (
	"context"
	"strings"
	"testing"
)

func TestMigrateCommandValidation(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "unknown command", args: []string{"explode"}, wantErr: "unknown migration command"},
		{name: "rollback needs confirmation", args: []string{"down"}, wantErr: "--yes"},
		{name: "missing DSN", args: []string{"status"}, wantErr: "DATABASE_URL"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "")
			err := migrate(context.Background(), test.args)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("migrate(%v) error = %v, want %q", test.args, err, test.wantErr)
			}
		})
	}
}
