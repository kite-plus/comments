// Package storetest gives each integration test a migrated store in its own
// PostgreSQL schema, so tests run in parallel against one database.
package storetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/kite-plus/comments/internal/store"
)

// EnvURL names the variable that points tests at a database.
const EnvURL = "COMMENTS_TEST_DATABASE_URL"

// New returns a migrated store in a fresh schema, dropped when the test
// ends. Without COMMENTS_TEST_DATABASE_URL the test is skipped.
func New(t testing.TB) *store.Store {
	t.Helper()
	s, _ := NewWithSchema(t)
	return s
}

// NewWithSchema is New that also returns the database URL and schema, for
// tests that start a second connection, such as a server under test.
func NewWithSchema(t testing.TB) (*store.Store, Schema) {
	t.Helper()
	url := os.Getenv(EnvURL)
	if url == "" {
		t.Skip(EnvURL + " is not set")
	}
	ctx := context.Background()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	schema := "test_" + hex.EncodeToString(b[:])

	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect to %s: %v", EnvURL, err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	s, err := store.OpenSchema(ctx, url, schema)
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
	return s, Schema{URL: url, Name: schema}
}

// Schema names a test's database and schema.
type Schema struct {
	URL  string
	Name string
}

// Conn opens a direct connection to a test's schema, for tests outside the
// store that need to look at or adjust rows.
func Conn(t testing.TB, s Schema) *pgx.Conn {
	t.Helper()
	cfg, err := pgx.ParseConfig(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = s.Name
	conn, err := pgx.ConnectConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

// Open creates and migrates a fresh schema outside a test, for the browser
// test harness; drop removes it.
func Open(ctx context.Context, url, prefix string) (s *store.Store, schema string, drop func(), err error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, "", nil, err
	}
	schema = prefix + hex.EncodeToString(b[:])
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		return nil, "", nil, err
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		_ = admin.Close(ctx)
		return nil, "", nil, err
	}
	drop = func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		_ = admin.Close(context.Background())
	}
	if s, err = store.OpenSchema(ctx, url, schema); err != nil {
		drop()
		return nil, "", nil, err
	}
	if err := s.Migrate(ctx); err != nil {
		s.Close()
		drop()
		return nil, "", nil, err
	}
	return s, schema, drop, nil
}
