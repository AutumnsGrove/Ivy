package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutumnsGrove/Ivy/config"
	"github.com/AutumnsGrove/Ivy/update"
)

// fakeRegistryAndCI stands in for GHCR (token + manifest) and the GitHub
// workflow-runs listing, so `ivy update` can be exercised without the network.
func fakeRegistryAndCI(t *testing.T, digest string) (ghcr, ci string) {
	t.Helper()
	ghcrMux := http.NewServeMux()
	ghcrMux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"token":"t"}`))
	})
	ghcrMux.HandleFunc("/v2/"+update.DefaultRepo+"/manifests/latest", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Docker-Content-Digest", digest)
		w.WriteHeader(http.StatusOK)
	})
	ghcrSrv := httptest.NewServer(ghcrMux)
	t.Cleanup(ghcrSrv.Close)

	ciMux := http.NewServeMux()
	ciMux.HandleFunc("/repos/"+update.DefaultRepo+"/actions/workflows/"+update.WorkflowFile+"/runs", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"workflow_runs": []map[string]string{{"status": "completed"}}})
	})
	ciSrv := httptest.NewServer(ciMux)
	t.Cleanup(ciSrv.Close)
	return ghcrSrv.URL, ciSrv.URL
}

func TestUpdateCommandRequestsTheLatestDigest(t *testing.T) {
	dir := t.TempDir()
	digest := "sha256:" + strings.Repeat("a", 64)
	ghcr, ci := fakeRegistryAndCI(t, digest)
	configPath := writeConfig(t, dir, "data_dir: "+dir+"\n")

	original := newUpdateClient
	newUpdateClient = func(cfg *config.Config) *update.Client {
		return &update.Client{
			Repo: update.DefaultRepo, SignalDir: cfg.UpdateSignalDir(), Token: cfg.Update.Token,
			GHCRBaseURL: ghcr, GitHubBaseURL: ci,
		}
	}
	t.Cleanup(func() { newUpdateClient = original })

	var out bytes.Buffer
	root := New("test")
	root.SetArgs([]string{"--config", configPath, "update"})
	root.SetOut(&out)
	if err := root.Execute(); err != nil {
		t.Fatalf("update: %v", err)
	}
	want := "ghcr.io/" + update.DefaultRepo + "@" + digest
	if !strings.Contains(out.String(), want) {
		t.Errorf("output = %q, want it to name %q", out.String(), want)
	}
	got, err := os.ReadFile(filepath.Join(dir, "update-signal", "requested"))
	if err != nil {
		t.Fatalf("reading signal: %v", err)
	}
	if string(got) != want {
		t.Errorf("signal = %q, want %q", got, want)
	}
}
