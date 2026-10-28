package instancelock

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func lockPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), ".caddy-view.lock")
}

func TestAcquireIsExclusive(t *testing.T) {
	ctx := context.Background()
	path := lockPath(t)

	first, err := Acquire(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(ctx, path); !errors.Is(err, ErrBusy) {
		t.Fatalf("a second Acquire error = %v, want ErrBusy", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	again, err := Acquire(ctx, path)
	if err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}
	defer again.Release()
}

func TestReleaseCanBeCalledTwice(t *testing.T) {
	l, err := Acquire(context.Background(), lockPath(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Errorf("the second Release: %v", err)
	}
}

func TestDifferentFilesDoNotCollide(t *testing.T) {
	ctx := context.Background()
	a, err := Acquire(ctx, lockPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	b, err := Acquire(ctx, lockPath(t))
	if err != nil {
		t.Fatalf("a lock on another file: %v", err)
	}
	defer b.Release()
}

func TestAcquireWithoutADirectoryIsNotBusy(t *testing.T) {
	_, err := Acquire(context.Background(), filepath.Join(t.TempDir(), "missing", ".caddy-view.lock"))
	if err == nil || errors.Is(err, ErrBusy) {
		t.Errorf("Acquire error = %v, want a failure that is not ErrBusy", err)
	}
}

func TestAcquireRefusesASymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "elsewhere")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, ".caddy-view.lock")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}
	if _, err := Acquire(context.Background(), link); err == nil || errors.Is(err, ErrBusy) {
		t.Errorf("Acquire error = %v, want a refusal that is not ErrBusy", err)
	}
}

func TestAcquireHonoursACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Acquire(ctx, lockPath(t)); !errors.Is(err, context.Canceled) {
		t.Errorf("Acquire error = %v, want context.Canceled", err)
	}
}

// The test binary runs itself as the process that holds the lock, so that a lock held by another process, and then
// by a process that was killed, is the real thing.
const holderEnv = "INSTANCELOCK_TEST_HOLD"

func TestMain(m *testing.M) {
	if path := os.Getenv(holderEnv); path != "" {
		l, err := Acquire(context.Background(), path)
		if err != nil {
			os.Stdout.WriteString("failed: " + err.Error() + "\n")
			os.Exit(1)
		}
		defer l.Release()
		os.Stdout.WriteString("locked\n")
		time.Sleep(time.Minute) // until the parent kills this process
		return
	}
	os.Exit(m.Run())
}

func TestAnotherProcessHoldsTheLockUntilItDies(t *testing.T) {
	if runtime.GOOS == "js" {
		t.Skip("no processes here")
	}
	path := lockPath(t)
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), holderEnv+"="+path)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill() // it may be gone already
		_ = cmd.Wait()
	})

	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "locked" {
		t.Fatalf("the holder said %q, %v, want it to have the lock", line, err)
	}
	if _, err := Acquire(context.Background(), path); !errors.Is(err, ErrBusy) {
		t.Fatalf("Acquire while another process holds it: error = %v, want ErrBusy", err)
	}

	// Killed, not released: the system must free the lock, with no stale file or PID left to clear by hand.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait() // the exit status of a killed process is not the point

	deadline := time.Now().Add(10 * time.Second)
	for {
		l, err := Acquire(context.Background(), path)
		if err == nil {
			l.Release()
			return
		}
		if !errors.Is(err, ErrBusy) || time.Now().After(deadline) {
			t.Fatalf("Acquire after the holder was killed: error = %v, want the lock to be free", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
