package watcher_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/update"
)

const target = "ghcr.io/autumnsgrove/ivy@sha256:" +
	"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// stubs stand in for the host tools update.sh drives. timeout, flock, sudo and
// sleep are stubbed so the script runs the same on a laptop that lacks the GNU
// ones and so a polling loop costs no real time; docker answers from the
// environment so each test scripts one failure.
var stubs = map[string]string{
	"timeout": "#!/bin/sh\nshift\nexec \"$@\"\n",
	"flock":   "#!/bin/sh\nexit 0\n",
	"sudo":    "#!/bin/sh\nexit 0\n",
	"sleep":   "#!/bin/sh\nexit 0\n",
	"git":     "#!/bin/sh\necho \"Already up to date.\"\nexit 0\n",
	"docker": `#!/bin/sh
echo "$@" >> "$STUB_DIR/docker.log"
case "$1 $2" in
"compose pull")
	printf '%b' "$DOCKER_PULL_OUT"
	exit "${DOCKER_PULL_RC:-0}"
	;;
"compose up") exit 0 ;;
"compose ps")
	[ -n "$DOCKER_PS_FAIL" ] && exit 1
	echo cid123
	exit 0
	;;
"compose logs") echo "log line"; exit 0 ;;
esac
if [ "$1" = inspect ]; then
	n=$(cat "$STUB_DIR/polls" 2>/dev/null || echo 0)
	n=$((n + 1))
	echo "$n" > "$STUB_DIR/polls"
	if [ "$n" -le "${DOCKER_STARTING_POLLS:-0}" ]; then echo starting; else echo healthy; fi
fi
exit 0
`,
}

type run struct {
	signalDir string
	envFile   string
	err       error
}

// runWatcher runs the real update.sh against a throwaway install directory
// with a pending request for target.
func runWatcher(t *testing.T, env ...string) run {
	t.Helper()
	return runWatcherFor(t, target, env...)
}

// runWatcherFor is runWatcher with the reference the signal file holds chosen
// by the test.
func runWatcherFor(t *testing.T, requested string, env ...string) run {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}
	install := t.TempDir()
	script, err := os.ReadFile("update.sh")
	if err != nil {
		t.Fatal(err)
	}
	scriptDir := filepath.Join(install, "compose", "watcher")
	signal := filepath.Join(install, "update-signal")
	bin := filepath.Join(install, "bin")
	for _, d := range []string{scriptDir, signal, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(scriptDir, "update.sh"), script, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	envFile := filepath.Join(install, ".env")
	if err := os.WriteFile(envFile, []byte("IVY_IMAGE=ghcr.io/autumnsgrove/ivy@sha256:"+strings.Repeat("0", 64)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(signal, "requested"), []byte(requested), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", filepath.Join(scriptDir, "update.sh"))
	cmd.Env = append([]string{"PATH=" + bin + ":/usr/bin:/bin", "STUB_DIR=" + install}, env...)
	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()
	var runErr error
	select {
	case runErr = <-done:
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("update.sh did not finish within 30s")
	}
	return run{signalDir: signal, envFile: envFile, err: runErr}
}

func (r run) requestRemains() bool { return update.Pending(r.signalDir) }

// docker compose output is not JSON-safe: tabs, carriage returns and escape
// sequences all turn up in pull errors. The result file is the only place the
// real reason an update failed is ever shown, so it has to stay valid JSON.
func TestFailureResultStaysValidJSONWhateverDockerPrints(t *testing.T) {
	t.Parallel()
	r := runWatcher(t,
		"DOCKER_PULL_RC=1",
		`DOCKER_PULL_OUT=Error response from daemon:\r\n\tpull access denied \x1b[31mfor ivy\x1b[0m\n`)

	res := update.ReadResult(r.signalDir)
	if res == nil {
		t.Fatal("the watcher's result is missing or not valid JSON, so the UI would show no reason")
	}
	if res.Status != "failed" || !strings.Contains(res.Detail, "pull access denied") {
		t.Errorf("result = %+v, want a failure that names the pull error", res)
	}
	if r.requestRemains() {
		t.Error("the request was left in place after a failed pull")
	}
}

// With `set -e`, any unguarded command that fails ends the script on the spot.
// The path unit fires whenever `requested` exists, so a file left behind makes
// the watcher re-run the whole pull-and-recreate over and over.
func TestRequestIsClearedEvenWhenTheScriptDies(t *testing.T) {
	t.Parallel()
	r := runWatcher(t, "DOCKER_PS_FAIL=1")

	if r.requestRemains() {
		t.Error("update-signal/requested still exists after the script died; the path unit would re-run the update forever")
	}
}

// A first start after an update can run migrations over a large mirror on the
// potato; the image's own healthcheck reports `starting` until it passes. A
// slow start is not a failed one, and rolling it back discards a good update.
func TestSlowStartingContainerIsNotRolledBack(t *testing.T) {
	t.Parallel()
	r := runWatcher(t, "DOCKER_STARTING_POLLS="+strconv.Itoa(60))

	res := update.ReadResult(r.signalDir)
	if res == nil || res.Status != "ok" {
		t.Fatalf("result = %+v, want ok for a container that took 60 polls to become healthy", res)
	}
	env, err := os.ReadFile(r.envFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), target) {
		t.Errorf(".env = %q, want it pinned to the new image", env)
	}
}

// The signal directory is world-writable so the container's uid can reach it
// through the bind mount, so any local user can drop a file there. The watcher
// runs docker as the deploy user, which makes the reference it pulls the whole
// trust boundary: only Ivy's own image may ever be accepted.
func TestForeignImageIsRefused(t *testing.T) {
	t.Parallel()
	foreign := "registry.example.net/someone/else@sha256:" + strings.Repeat("a", 64)
	r := runWatcherFor(t, foreign)

	res := update.ReadResult(r.signalDir)
	if res == nil || res.Status != "failed" {
		t.Fatalf("result = %+v, want a refusal", res)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(r.signalDir), "docker.log")); err == nil {
		t.Error("docker ran for an image that is not Ivy's")
	}
	if r.requestRemains() {
		t.Error("the refused request was left in place")
	}
}
