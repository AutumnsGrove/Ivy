package cmd

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeConfig(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "ivy.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInitCreatesDatabases(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configPath := writeConfig(t, dir, "data_dir: "+dir+"\n")

	root := New("test")
	root.SetArgs([]string{"--config", configPath, "init"})
	root.SetOut(os.Stderr)
	if err := root.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}
	for _, name := range []string{"mirror.db", "state.db"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s missing after init: %v", name, err)
		}
	}
}

func TestDoctorSucceeds(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configPath := writeConfig(t, dir, "data_dir: "+dir+"\n")

	root := New("test")
	root.SetArgs([]string{"--config", configPath, "doctor"})
	root.SetOut(os.Stderr)
	if err := root.Execute(); err != nil {
		t.Fatalf("doctor: %v", err)
	}
}

func TestRunServesAndShutsDown(t *testing.T) {
	t.Parallel()
	addr := freeAddr(t)
	dir := t.TempDir()
	configPath := writeConfig(t, dir, "listen: "+addr+"\ndata_dir: "+dir+"\n")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	root := New("test")
	root.SetArgs([]string{"--config", configPath, "run"})
	root.SetOut(os.Stderr)
	root.SetErr(os.Stderr)

	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()

	waitForHealth(t, "http://"+addr+"/api/v1/health")

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not shut down")
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func waitForHealth(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never became healthy", url)
}
