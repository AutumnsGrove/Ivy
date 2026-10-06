package cmd

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/config"
	"github.com/AutumnsGrove/Ivy/internal/accountsvc"
	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/store"
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

// An open event stream is never idle, so http.Server.Shutdown would wait out its
// whole timeout for it. The run command must close the hub on shutdown.
func TestRunShutsDownPromptlyWithAnEventStreamOpen(t *testing.T) {
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

	resp, err := http.Get("http://" + addr + "/api/v1/events")
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d, want 200", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned %v, want a clean shutdown", err)
		}
	case <-time.After(4 * time.Second): // under Shutdown's own 5 s timeout
		t.Fatal("run did not shut down while a stream was open")
	}
}

// `ivy run` owns a sync worker per configured account, so the deployed binary
// keeps the mirror fresh on its own (no separate sync process).
func TestRunSyncsAConfiguredAccount(t *testing.T) {
	addr := freeAddr(t)
	dir := t.TempDir()
	w, err := mailworld.New()
	if err != nil {
		t.Fatalf("mailworld: %v", err)
	}
	defer func() { _ = w.Close() }()
	acc := w.Account("me@grove.test", "secret")
	acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("hello").Build())
	host, portStr, err := net.SplitHostPort(w.IMAPAddr())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.PasswordEnv("acct-1"), "secret")
	configPath := writeConfig(t, dir, fmt.Sprintf(`listen: %s
data_dir: %s
accounts:
  - id: acct-1
    address: me@grove.test
    imap_host: %s
    imap_port: %s
    smtp_host: %s
    smtp_port: 1
    username: me@grove.test
    insecure: true
`, addr, dir, host, portStr, host))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := New("test")
	root.SetArgs([]string{"--config", configPath, "run"})
	root.SetOut(os.Stderr)
	root.SetErr(os.Stderr)
	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()
	waitForHealth(t, "http://"+addr+"/api/v1/health")

	dbs, err := store.Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = dbs.Close() }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if f, err := dbs.GetFolderByName(context.Background(), "acct-1", "INBOX"); err == nil {
			if uids, err := dbs.MessageUIDs(context.Background(), f.ID); err == nil && len(uids) == 1 {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if f, err := dbs.GetFolderByName(context.Background(), "acct-1", "INBOX"); err != nil {
		t.Fatal("the run command never synced the configured account")
	} else if uids, _ := dbs.MessageUIDs(context.Background(), f.ID); len(uids) != 1 {
		t.Fatalf("INBOX holds %v, want the delivered message", uids)
	}

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

// The point of in-app setup: `ivy run` with no accounts configured at all, the
// operator types the mailbox into the app, and mail starts flowing without a
// restart. The password lands in a private file, not in ivy.yaml or the DB.
// Not parallel: it swaps the package's provider for the fake mail world.
func TestRunConnectsAnAccountTypedIntoTheApp(t *testing.T) {
	addr := freeAddr(t)
	dir := t.TempDir()
	w, err := mailworld.New()
	if err != nil {
		t.Fatalf("mailworld: %v", err)
	}
	defer func() { _ = w.Close() }()
	w.Account("me@grove.test", "secret").Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("hello").Build())
	host, portStr, err := net.SplitHostPort(w.IMAPAddr())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	prev := appProvider
	appProvider = accountsvc.Provider{IMAPHost: host, IMAPPort: port, SMTPHost: host, SMTPPort: 1, Insecure: true}
	t.Cleanup(func() { appProvider = prev })

	configPath := writeConfig(t, dir, fmt.Sprintf("listen: %s\ndata_dir: %s\n", addr, dir))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := New("test")
	root.SetArgs([]string{"--config", configPath, "run"})
	root.SetOut(os.Stderr)
	root.SetErr(os.Stderr)
	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()
	waitForHealth(t, "http://"+addr+"/api/v1/health")

	resp, err := http.Post("http://"+addr+"/api/v1/accounts", "application/json",
		strings.NewReader(`{"address":"me@grove.test","password":"secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /accounts status = %d, want 201", resp.StatusCode)
	}

	dbs, err := store.Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = dbs.Close() }()
	deadline := time.Now().Add(5 * time.Second)
	synced := false
	for time.Now().Before(deadline) && !synced {
		if f, err := dbs.GetFolderByName(context.Background(), "purelymail", "INBOX"); err == nil {
			if uids, err := dbs.MessageUIDs(context.Background(), f.ID); err == nil && len(uids) == 1 {
				synced = true
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !synced {
		t.Fatal("the account typed into the app never synced")
	}
	info, err := os.Stat(filepath.Join(dir, "secrets", "purelymail"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("password file = %v, %v; want a mode 600 file", info, err)
	}
	if yaml, _ := os.ReadFile(configPath); strings.Contains(string(yaml), "secret") {
		t.Error("the password reached ivy.yaml")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not shut down with a worker started from the API")
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

// The real server, not just the handler: allowed_hosts must reach the gateway
// the run command builds, or the rebinding guard is dead code.
func TestRunRefusesAForeignHostUnlessAllowed(t *testing.T) {
	t.Parallel()
	addr := freeAddr(t)
	dir := t.TempDir()
	configPath := writeConfig(t, dir,
		"listen: "+addr+"\ndata_dir: "+dir+"\nallowed_hosts:\n  - ivy.tail1234.ts.net\n")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := New("test")
	root.SetArgs([]string{"--config", configPath, "run"})
	root.SetOut(os.Stderr)
	root.SetErr(os.Stderr)
	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()
	waitForHealth(t, "http://"+addr+"/api/v1/health")

	for host, want := range map[string]int{
		"evil.example":          http.StatusForbidden,
		"ivy.tail1234.ts.net":   http.StatusOK,
		"localhost:12345":       http.StatusOK,
		"ivy.tail1234.ts.net.x": http.StatusForbidden,
	} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/api/v1/version", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("Host %q: status = %d, want %d", host, resp.StatusCode, want)
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not shut down")
	}
}

// The operator needs to see which names the API answers to, and be told what to
// do when it will be reached over a network name that is not listed.
func TestInitAndDoctorReportAllowedHosts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configPath := writeConfig(t, dir,
		"listen: 100.64.0.7:8787\ndata_dir: "+dir+"\nallowed_hosts:\n  - ivy.tail1234.ts.net\n")
	bare := writeConfig(t, t.TempDir(), "listen: 100.64.0.7:8787\ndata_dir: "+dir+"\n")

	for _, name := range []string{"init", "doctor"} {
		var out bytes.Buffer
		root := New("test")
		root.SetArgs([]string{"--config", configPath, name})
		root.SetOut(&out)
		if err := root.Execute(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(out.String(), "ivy.tail1234.ts.net") || !strings.Contains(out.String(), "100.64.0.7") {
			t.Errorf("%s output = %q, want the allowed hosts listed", name, out.String())
		}
		if strings.Contains(out.String(), "set allowed_hosts") {
			t.Errorf("%s output = %q, want no hint when a name is configured", name, out.String())
		}
	}

	// A network listen address with no configured name gets the hint.
	var out bytes.Buffer
	root := New("test")
	root.SetArgs([]string{"--config", bare, "init"})
	root.SetOut(&out)
	if err := root.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(out.String(), "set allowed_hosts") {
		t.Errorf("init output = %q, want a hint to set allowed_hosts", out.String())
	}
}
