package tests

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/divijg19/Verse/internal/database"
	appserver "github.com/divijg19/Verse/internal/server"
	"github.com/google/uuid"
)

func requireTestDSN(t *testing.T) string {
	t.Helper()

	dsn := strings.TrimSpace(os.Getenv("VERSE_E2E_DATABASE_URL"))
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("DATABASE_URL"))
	}
	if dsn == "" {
		t.Skip("set VERSE_E2E_DATABASE_URL (or DATABASE_URL) to run database-backed tests")
	}

	return dsn
}

func connectTestDB(t *testing.T) {
	t.Helper()

	dsn := requireTestDSN(t)
	t.Setenv("DATABASE_URL", dsn)

	if database.Pool != nil {
		database.Pool.Close()
		database.Pool = nil
	}

	if err := database.Connect(); err != nil {
		t.Fatalf("database connect failed: %v", err)
	}

	t.Cleanup(func() {
		if database.Pool != nil {
			database.Pool.Close()
			database.Pool = nil
		}
	})
}

func truncatePoems(t *testing.T) {
	t.Helper()

	if database.Pool == nil {
		t.Fatalf("database pool is nil")
	}

	if _, err := database.Pool.Exec(context.Background(), `TRUNCATE TABLE poems`); err != nil {
		t.Fatalf("truncate poems failed: %v", err)
	}
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}

	if filepath.Base(wd) == "tests" {
		if err := os.Chdir(".."); err != nil {
			t.Fatalf("chdir to repo root failed: %v", err)
		}
		t.Cleanup(func() {
			_ = os.Chdir(wd)
		})
	}

	return httptest.NewServer(appserver.NewRouter())
}

func get(t *testing.T, endpoint string, headers map[string]string) (int, string, http.Header) {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatalf("create GET request failed: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("execute GET request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read GET response failed: %v", err)
	}

	return resp.StatusCode, string(body), resp.Header
}

func postForm(t *testing.T, endpoint string, values url.Values, headers map[string]string) (int, string, http.Header) {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatalf("create POST request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("execute POST request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read POST response failed: %v", err)
	}

	return resp.StatusCode, string(body), resp.Header
}

func insertPoem(t *testing.T, content string) string {
	t.Helper()

	if database.Pool == nil {
		t.Fatalf("database pool is nil")
	}

	id := uuid.NewString()
	if _, err := database.Pool.Exec(context.Background(), `INSERT INTO poems (id, content) VALUES ($1, $2)`, id, content); err != nil {
		t.Fatalf("insert poem failed: %v", err)
	}

	return id
}

func insertPoemAt(t *testing.T, content string, createdAt time.Time) string {
	t.Helper()

	if database.Pool == nil {
		t.Fatalf("database pool is nil")
	}

	id := uuid.NewString()
	if _, err := database.Pool.Exec(context.Background(), `INSERT INTO poems (id, content, created_at) VALUES ($1, $2, $3)`, id, content, createdAt.UTC()); err != nil {
		t.Fatalf("insert timed poem failed: %v", err)
	}

	return id
}

func markPoemDeleted(t *testing.T, id string) {
	t.Helper()

	if database.Pool == nil {
		t.Fatalf("database pool is nil")
	}

	if _, err := database.Pool.Exec(context.Background(), `UPDATE poems SET deleted_at = NOW() WHERE id = $1`, id); err != nil {
		t.Fatalf("soft delete poem failed: %v", err)
	}
}

func poemContentByID(t *testing.T, id string) string {
	t.Helper()

	if database.Pool == nil {
		t.Fatalf("database pool is nil")
	}

	var content string
	if err := database.Pool.QueryRow(context.Background(), `SELECT content FROM poems WHERE id = $1`, id).Scan(&content); err != nil {
		t.Fatalf("query poem content failed: %v", err)
	}

	return content
}

func poemDeletedByID(t *testing.T, id string) bool {
	t.Helper()

	if database.Pool == nil {
		t.Fatalf("database pool is nil")
	}

	var deleted bool
	if err := database.Pool.QueryRow(context.Background(), `SELECT deleted_at IS NOT NULL FROM poems WHERE id = $1`, id).Scan(&deleted); err != nil {
		t.Fatalf("query poem deleted status failed: %v", err)
	}

	return deleted
}
