package devstack

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FileStamp is enough to notice an edit even when mtime resolution hides it.
type FileStamp struct {
	Mod  time.Time
	Size int64
}

// ScanGo records every Go source and module file under root, skipping trees
// that never hold Ivy source (the dev dir, web, spikes, generated output).
func ScanGo(root string) (map[string]FileStamp, error) {
	out := make(map[string]FileStamp)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path == root {
				return nil
			}
			switch name {
			case ".git", ".dev", "node_modules", "web", "spikes", "dist", "build", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(name, ".go") || name == "go.mod" || name == "go.sum" {
			info, err := d.Info()
			if err != nil {
				return err
			}
			out[path] = FileStamp{Mod: info.ModTime(), Size: info.Size()}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Changed lists paths that were added, removed or edited between two scans.
func Changed(prev, next map[string]FileStamp) []string {
	var changed []string
	for path, stamp := range next {
		if before, ok := prev[path]; !ok || before != stamp {
			changed = append(changed, path)
		}
	}
	for path := range prev {
		if _, ok := next[path]; !ok {
			changed = append(changed, path)
		}
	}
	sort.Strings(changed)
	return changed
}

// ModuleRoot walks up from dir to the nearest go.mod, so ivy-dev can build the
// binary regardless of where it was invoked.
func ModuleRoot(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(abs, "go.mod")); err == nil {
			return abs, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", fmt.Errorf("devstack: no go.mod above %s", dir)
		}
		abs = parent
	}
}

// Supervisor runs the Ivy binary and restarts it when Go files under Root
// change, rebuilding first. Build and Command are injected so the loop is
// tested without compiling the whole module.
type Supervisor struct {
	Root         string
	Build        func(context.Context) error
	Command      func() *exec.Cmd
	PollInterval time.Duration
	Log          io.Writer
}

// Run owns the child build/restart loop until ctx is cancelled. It returns nil
// on cancellation, never leaving a child running.
func (s *Supervisor) Run(ctx context.Context) error {
	interval := s.PollInterval
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	logw := s.Log
	if logw == nil {
		logw = io.Discard
	}

	for {
		// Scanned before the build, not after: a file saved while the compiler
		// runs is newer than the binary it produces and must trigger a rebuild.
		baseline, _ := ScanGo(s.Root)
		if err := s.Build(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			fmt.Fprintf(logw, "build failed: %v\n", err)
			if !s.awaitChange(ctx, interval, baseline) {
				return nil
			}
			continue
		}
		cmd := s.Command()
		cmd.Stdout = logw
		cmd.Stderr = logw
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(logw, "start failed: %v\n", err)
			if !s.awaitChange(ctx, interval, baseline) {
				return nil
			}
			continue
		}
		childDone := make(chan error, 1)
		go func() { childDone <- cmd.Wait() }()
		ticker := time.NewTicker(interval)

		restart := false
		for !restart {
			select {
			case <-ctx.Done():
				_ = cmd.Process.Kill()
				<-childDone
				ticker.Stop()
				return nil
			case <-childDone:
				fmt.Fprintln(logw, "child exited, waiting for a Go change")
				ticker.Stop()
				if !s.awaitChange(ctx, interval, baseline) {
					return nil
				}
				restart = true
			case <-ticker.C:
				next, _ := ScanGo(s.Root)
				if len(Changed(baseline, next)) > 0 {
					fmt.Fprintln(logw, "Go change detected, restarting")
					_ = cmd.Process.Kill()
					<-childDone
					ticker.Stop()
					restart = true
				}
			}
		}
	}
}

// awaitChange blocks until a Go file differs from baseline or ctx ends,
// reporting false when ctx ended so Run can return.
func (s *Supervisor) awaitChange(ctx context.Context, interval time.Duration, baseline map[string]FileStamp) bool {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			next, _ := ScanGo(s.Root)
			if len(Changed(baseline, next)) > 0 {
				return true
			}
		}
	}
}
