package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// openLogStream opens the SSE route and returns the reader once the stream has announced itself.
func openLogStream(t *testing.T, base string) *bufio.Reader {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/logs/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/logs/stream: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	br := bufio.NewReader(resp.Body)
	waitForLine(t, br, ": connected")
	return br
}

// waitForLine reads lines until one equals want.
func waitForLine(t *testing.T, br *bufio.Reader, want string) {
	t.Helper()
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("reading the stream while waiting for %q: %v", want, err)
		}
		if strings.TrimRight(line, "\r\n") == want {
			return
		}
	}
}

func appendToFile(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

func TestServeStreamsAnExplicitAccessLog(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "access.log")
	if err := os.WriteFile(logPath, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	admin := fakeAdmin(t, goodConfig)
	base, stop, _, _ := startServe(t, "--admin", admin.URL, "--access-log", logPath)
	defer stop()

	br := openLogStream(t, base)
	appendToFile(t, logPath, `{"status":200}`+"\n")
	waitForLine(t, br, `data: {"status":200}`)
}

func TestServeDiscoversTheAccessLogFromCaddy(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "access.log")
	if err := os.WriteFile(logPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := json.Marshal(map[string]any{"logging": map[string]any{"logs": map[string]any{"log0": map[string]any{
		"writer":  map[string]any{"output": "file", "filename": logPath},
		"encoder": map[string]any{"format": "json"},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	admin := fakeAdmin(t, string(cfg))
	base, stop, _, _ := startServe(t, "--admin", admin.URL)
	defer stop()

	br := openLogStream(t, base)
	appendToFile(t, logPath, "discovered\n")
	waitForLine(t, br, "data: discovered")
}

func TestServeRejectsAnAccessLogThatIsADirectory(t *testing.T) {
	code, _, stderr := run(t, "serve", "--listen", "127.0.0.1:0", "--access-log", t.TempDir())
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "invalid --access-log") {
		t.Errorf("stderr %q does not explain the problem with --access-log", stderr)
	}
}

func TestServeAcceptsAnAccessLogThatDoesNotExistYet(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "later.log")
	admin := fakeAdmin(t, goodConfig)
	base, stop, _, _ := startServe(t, "--admin", admin.URL, "--access-log", logPath)
	defer stop()

	br := openLogStream(t, base)
	if err := os.WriteFile(logPath, []byte("created\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForLine(t, br, "data: created")
}
