package agent

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTailFileReturnsLastNLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	var lines []string
	for i := 1; i <= 10; i++ {
		lines = append(lines, "line"+string(rune('0'+i%10)))
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write log file: %v", err)
	}

	got, err := tailFile(path, 3)
	if err != nil {
		t.Fatalf("tailFile: %v", err)
	}
	gotLines := strings.Split(strings.TrimRight(string(got), "\n"), "\n")
	if len(gotLines) != 3 {
		t.Fatalf("got %d lines, want 3: %q", len(gotLines), got)
	}
	wantLast3 := lines[len(lines)-3:]
	for i, want := range wantLast3 {
		if gotLines[i] != want {
			t.Errorf("line %d = %q, want %q", i, gotLines[i], want)
		}
	}
}

func TestTailFileMissingReturnsNotExist(t *testing.T) {
	_, err := tailFile(filepath.Join(t.TempDir(), "missing.log"), 10)
	if !os.IsNotExist(err) {
		t.Errorf("err = %v, want a not-exist error", err)
	}
}

func TestLogHandlerServesLogFile(t *testing.T) {
	dir := t.TempDir()
	id := containerNameRaw("default", "web-0")
	if err := os.WriteFile(logFilePath(dir, id), []byte("hello from container\nsecond line\n"), 0o644); err != nil {
		t.Fatalf("write log file: %v", err)
	}

	srv := httptest.NewServer(NewLogHandler(dir))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/logs?namespace=default&name=web-0")
	if err != nil {
		t.Fatalf("GET /logs: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "hello from container") {
		t.Errorf("body = %q, want it to contain the log content", body)
	}
}

func TestLogHandlerReturns404ForMissingLogs(t *testing.T) {
	srv := httptest.NewServer(NewLogHandler(t.TempDir()))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/logs?namespace=default&name=nonexistent")
	if err != nil {
		t.Fatalf("GET /logs: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestLogHandlerRequiresNamespaceAndName(t *testing.T) {
	srv := httptest.NewServer(NewLogHandler(t.TempDir()))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/logs?namespace=default")
	if err != nil {
		t.Fatalf("GET /logs: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}
