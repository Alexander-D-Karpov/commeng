// Package migrations owns the versioned PostgreSQL schema changes.
// SQL files are embedded into the binary, so deployment does not require
// copying a migrations directory to the runtime container.
package migrations

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

//go:embed *.up.sql *.down.sql
var files embed.FS

var filenamePattern = regexp.MustCompile(`^([0-9]{3})_([a-z][a-z0-9_]*)\.(up|down)\.sql$`)

// Migration is an immutable, numbered pair of schema changes.
// The checksum covers the Up SQL; applied migrations must not be edited.
type Migration struct {
	Version  int64
	Name     string
	UpSQL    string
	DownSQL  string
	Checksum string
}

// All validates the embedded migration history before any database work.
func All() ([]Migration, error) {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}

	steps := make(map[int64]*Migration)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		match := filenamePattern.FindStringSubmatch(entry.Name())
		if match == nil {
			return nil, fmt.Errorf("invalid migration filename %q", entry.Name())
		}
		version, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil || version == 0 {
			return nil, fmt.Errorf("invalid migration version in %q", entry.Name())
		}
		content, err := fs.ReadFile(files, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", entry.Name(), err)
		}
		if !hasSQLStatement(string(content)) {
			return nil, fmt.Errorf("migration %s has no SQL statements", entry.Name())
		}
		step := steps[version]
		if step == nil {
			step = &Migration{Version: version, Name: match[2]}
			steps[version] = step
		}
		if step.Name != match[2] {
			return nil, fmt.Errorf("inconsistent name for migration %03d", version)
		}
		switch match[3] {
		case "up":
			if step.UpSQL != "" {
				return nil, fmt.Errorf("duplicate up migration %03d", version)
			}
			step.UpSQL = string(content)
		case "down":
			if step.DownSQL != "" {
				return nil, fmt.Errorf("duplicate down migration %03d", version)
			}
			step.DownSQL = string(content)
		}
	}

	versions := make([]int64, 0, len(steps))
	for version := range steps {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })

	result := make([]Migration, 0, len(versions))
	for index, version := range versions {
		step := *steps[version]
		if version != int64(index)+1 {
			return nil, fmt.Errorf("migration versions must be contiguous from 001; missing %03d", index+1)
		}
		if step.UpSQL == "" || step.DownSQL == "" {
			return nil, fmt.Errorf("migration %03d must have .up.sql and .down.sql", version)
		}
		sum := sha256.Sum256([]byte(step.UpSQL))
		step.Checksum = hex.EncodeToString(sum[:])
		result = append(result, step)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("no embedded migrations")
	}
	return result, nil
}

// hasSQLStatement rejects freshly scaffolded migrations that still contain
// only comments. Such files must never be committed as a successfully applied
// schema revision.
func hasSQLStatement(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "--") {
			return true
		}
	}
	return false
}
