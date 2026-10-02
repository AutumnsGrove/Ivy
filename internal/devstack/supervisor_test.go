package devstack_test

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/internal/devstack"
)

func TestModuleRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := devstack.ModuleRoot(nested)
	if err != nil {
		t.Fatalf("ModuleRoot: %v", err)
	}
	if got != root {
		t.Fatalf("ModuleRoot = %s, want %s", got, root)
	}
	if _, err := devstack.ModuleRoot(t.TempDir()); err == nil {
		t.Fatal("expected an error with no go.mod above")
	}
}

func TestScanGoDetectsChanges(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	file := filepath.Join(root, "main.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := devstack.ScanGo(root)
	if err != nil {
		t.Fatalf("ScanGo: %v", err)
	}
	if len(devstack.Changed(first, first)) != 0 {
		t.Fatal("a scan should equal itself")
	}
	// Same mtime, different size: size must still register as a change.
	if err := os.WriteFile(file, []byte("package main\n// changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := devstack.ScanGo(root)
	if err != nil {
		t.Fatalf("ScanGo: %v", err)
	}
	if changed := devstack.Changed(first, second); len(changed) != 1 || changed[0] != file {
		t.Fatalf("Changed = %v, want [%s]", changed, file)
	}
}

func TestSupervisorRestartsOnGoChange(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	file := filepath.Join(root, "main.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	starts := 0
	sup := &devstack.Supervisor{
		Root:  root,
		Build: func(context.Context) error { return nil },
		Command: func() *exec.Cmd {
			mu.Lock()
			starts++
			mu.Unlock()
			return exec.Command("sleep", "60")
		},
		PollInterval: 20 * time.Millisecond,
		Log:          io.Discard,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()

	waitFor(t, "first start", func() bool { return startsCount(&mu, &starts) == 1 })
	// Let the supervisor capture its baseline before editing, or the change is
	// absorbed into it.
	time.Sleep(3 * sup.PollInterval)
	if err := os.WriteFile(file, []byte("package main\n// changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "restart", func() bool { return startsCount(&mu, &starts) >= 2 })

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Supervisor.Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor did not stop")
	}
}

func TestSupervisorStopsCleanlyWithoutChanges(t *testing.T) {
	t.Parallel()
	sup := &devstack.Supervisor{
		Root:         t.TempDir(),
		Build:        func(context.Context) error { return nil },
		Command:      func() *exec.Cmd { return exec.Command("sleep", "60") },
		PollInterval: 20 * time.Millisecond,
		Log:          io.Discard,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()
	time.Sleep(80 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Supervisor.Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor did not stop")
	}
}

func startsCount(mu *sync.Mutex, starts *int) int {
	mu.Lock()
	defer mu.Unlock()
	return *starts
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
