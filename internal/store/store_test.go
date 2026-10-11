package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

var update = flag.Bool("update", false, "rewrite golden files")

// newTestStore mirrors storetest.New, which this package cannot import.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("COMMENTS_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("COMMENTS_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	schema := "test_" + hex.EncodeToString(b[:])
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	s, err := OpenSchema(ctx, url, schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		_ = admin.Close(context.Background())
	})
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func (s *Store) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := s.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

// TestSchemaColumns is the schema guard of data-model.md section 5: a new
// column fails it until testdata/schema/columns.txt and the design say so.
func TestSchemaColumns(t *testing.T) {
	s := newTestStore(t)
	rows, err := s.pool.Query(context.Background(), `
		SELECT table_name || '.' || column_name || ' ' || data_type
		FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name NOT LIKE 'goose%'
		ORDER BY table_name, column_name`)
	if err != nil {
		t.Fatal(err)
	}
	cols, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	got := []byte(strings.Join(cols, "\n") + "\n")
	path := filepath.Join("..", "..", "testdata", "schema", "columns.txt")
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("schema columns changed; review against data-model.md section 1, then run go test -update\n%s", got)
	}
	// Section 1 of data-model.md: things the database never holds.
	banned := map[string]bool{"ip": true, "ip_address": true, "user_agent": true, "email": true, "password": true,
		"password_hash": true, "content": true, "page_content": true, "article": true, "referrer": true}
	for _, c := range cols {
		_, col, _ := strings.Cut(strings.Fields(c)[0], ".")
		if banned[col] || strings.HasSuffix(col, "_ip") || (strings.HasSuffix(col, "_email") && !strings.HasSuffix(c, " boolean")) {
			t.Errorf("column %s looks like data the schema must never hold", c)
		}
	}
}
