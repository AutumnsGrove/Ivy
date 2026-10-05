// Package update is the self-update path for a container deployment: it
// resolves the published image's digest from GHCR, waits out an in-flight CI
// build so a click right after a merge cannot deliver the previous build, and
// hands the target to the host-side watcher through a signal file. It never
// pulls or restarts anything itself; that stays on the host, outside the
// container (ARCHITECTURE.md 9).
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// The image Ivy ships as. Hardcoded on purpose: like the model registry, it
// names what this software is, not an operator preference (round 57).
const (
	DefaultRepo  = "autumnsgrove/ivy"
	DefaultTag   = "latest"
	WorkflowFile = "docker-publish.yml"
)

// maxResponseBytes bounds how much of a third-party response is buffered.
// GitHub and GHCR are trusted, but a response this size is never legitimate
// JSON either way.
const maxResponseBytes = 10 << 20

// Base URLs are vars so tests can point them at httptest servers.
var (
	ghcrBaseURL   = "https://ghcr.io"
	githubBaseURL = "https://api.github.com"
)

// Timings are vars so a test can shrink them instead of a timeout path taking
// real minutes. ciMaxWait comfortably exceeds a multi-arch publish plus a cold
// cache.
var (
	ciPollInterval = 10 * time.Second
	ciMaxWait      = 6 * time.Minute
)

const requestTimeout = 15 * time.Second

// Result mirrors what the host watcher's update.sh writes to the signal
// directory: its own account of the most recent attempt, including the real
// failure detail this process can never capture on its own.
type Result struct {
	Status     string `json:"status"`
	Detail     string `json:"detail"`
	Target     string `json:"target"`
	FinishedAt string `json:"finished_at"`
}

// Client drives one update request. The zero value is not usable; set Repo and
// SignalDir.
type Client struct {
	// Repo is the GHCR repository, without the registry host.
	Repo string
	// SignalDir is the directory the container and the host watcher share.
	SignalDir string
	// Token is an optional GitHub token for the Actions API; empty is allowed
	// and simply has a lower rate limit.
	Token string
	// GHCRBaseURL and GitHubBaseURL default to the real hosts. They are
	// exported so a caller (a test, or a deployment behind a mirror) can point
	// the client elsewhere.
	GHCRBaseURL   string
	GitHubBaseURL string
}

// repo returns the configured repository or the default.
func (c *Client) repo() string {
	if c.Repo != "" {
		return c.Repo
	}
	return DefaultRepo
}

func (c *Client) ghcrBase() string {
	if c.GHCRBaseURL != "" {
		return c.GHCRBaseURL
	}
	return ghcrBaseURL
}

func (c *Client) githubBase() string {
	if c.GitHubBaseURL != "" {
		return c.GitHubBaseURL
	}
	return githubBaseURL
}

// Request waits out an in-flight publish, resolves the image digest, and writes
// the host watcher's signal file. It returns the exact reference the watcher
// will pull. It does not wait for the watcher to finish.
func (c *Client) Request(ctx context.Context) (string, error) {
	repo := c.repo()
	if c.SignalDir == "" {
		return "", errors.New("update: no signal directory configured")
	}
	waitForPublishWorkflowAt(ctx, c.githubBase(), repo, c.Token)

	digest, err := resolveDigestAt(ctx, c.ghcrBase(), repo, DefaultTag)
	if err != nil {
		return "", err
	}
	if !validDigest(digest) {
		return "", fmt.Errorf("update: registry returned %q, not a sha256 digest", digest)
	}
	target := ghcrHost + "/" + repo + "@" + digest
	if err := WriteSignal(c.SignalDir, target); err != nil {
		return "", err
	}
	return target, nil
}

// ghcrHost is the registry hostname in a target reference.
const ghcrHost = "ghcr.io"

// digestRE is the only shape the host watcher may ever pull; anything else is a
// bug upstream, and failing fast beats writing garbage into the signal file.
var digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func validDigest(d string) bool { return digestRE.MatchString(d) }

// ResolveDigest queries GHCR's OCI Distribution API for repo:tag's current
// manifest digest, with an anonymous pull token and a plain HEAD — no docker
// CLI or socket needed.
func ResolveDigest(ctx context.Context, repo, tag string) (string, error) {
	return resolveDigestAt(ctx, ghcrBaseURL, repo, tag)
}

func resolveDigestAt(ctx context.Context, base, repo, tag string) (string, error) {
	token, err := anonymousToken(ctx, base, repo)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, base+"/v2/"+repo+"/manifests/"+tag, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	// Every manifest media type GHCR might serve for a multi-arch tag, plus the
	// plain v2 manifest as a single-arch fallback.
	req.Header.Set("Accept", "application/vnd.oci.image.index.v1+json,application/vnd.docker.distribution.manifest.list.v2+json,application/vnd.docker.distribution.manifest.v2+json")

	resp, err := newHTTPClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("update: checking manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("update: manifest check failed (status %d)", resp.StatusCode)
	}
	digest := resp.Header.Get("Docker-Content-Digest")
	if digest == "" {
		return "", errors.New("update: registry response had no Docker-Content-Digest")
	}
	return digest, nil
}

// anonymousToken exchanges for a pull-scoped anonymous token; GHCR requires one
// even for public images.
func anonymousToken(ctx context.Context, base, repo string) (string, error) {
	q := url.Values{}
	q.Set("service", "ghcr.io")
	q.Set("scope", "repository:"+repo+":pull")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/token?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	resp, err := newHTTPClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("update: getting ghcr token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("update: ghcr token request failed (status %d)", resp.StatusCode)
	}
	body, err := readBounded(resp.Body)
	if err != nil {
		return "", err
	}
	var parsed struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("update: decoding token response: %w", err)
	}
	if parsed.Token == "" {
		return "", errors.New("update: ghcr token response had no token")
	}
	return parsed.Token, nil
}

// waitForPublishWorkflow blocks while the most recent publish run for main is
// queued or in progress, so the digest resolved afterwards is the build that is
// actually in flight. Deliberately best-effort: any API failure returns at once
// rather than making "update" fail over a GitHub hiccup.
func waitForPublishWorkflow(ctx context.Context, repo, token string) {
	waitForPublishWorkflowAt(ctx, githubBaseURL, repo, token)
}

func waitForPublishWorkflowAt(ctx context.Context, base, repo, token string) {
	deadline := time.Now().Add(ciMaxWait)
	for {
		inProgress, err := publishWorkflowInProgress(ctx, base, repo, token)
		if err != nil {
			slog.WarnContext(ctx, "checking publish workflow status failed, proceeding", "error", err)
			return
		}
		if !inProgress {
			return
		}
		if time.Now().After(deadline) {
			slog.WarnContext(ctx, "publish workflow still running after max wait, proceeding", "waited", ciMaxWait)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(ciPollInterval):
		}
	}
}

// publishWorkflowInProgress reports whether the newest publish run for main is
// queued or in progress. A token is optional; it only raises the rate limit.
func publishWorkflowInProgress(ctx context.Context, base, repo, token string) (bool, error) {
	u := base + "/repos/" + repo + "/actions/workflows/" + WorkflowFile + "/runs?branch=main&per_page=1"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := newHTTPClient().Do(req)
	if err != nil {
		return false, fmt.Errorf("update: fetching workflow runs: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("update: github actions api status %d", resp.StatusCode)
	}
	body, err := readBounded(resp.Body)
	if err != nil {
		return false, err
	}
	var parsed struct {
		WorkflowRuns []struct {
			Status string `json:"status"`
		} `json:"workflow_runs"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return false, fmt.Errorf("update: decoding workflow runs: %w", err)
	}
	if len(parsed.WorkflowRuns) == 0 {
		return false, nil
	}
	status := parsed.WorkflowRuns[0].Status
	return status == "queued" || status == "in_progress", nil
}

// WriteSignal atomically writes target to dir/requested. The host watcher's
// path unit fires the instant the file exists, so a partial write would hand it
// a truncated reference.
func WriteSignal(dir, target string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("update: creating signal directory: %w", err)
	}
	dest := filepath.Join(dir, "requested")
	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, []byte(target), 0o600); err != nil {
		return fmt.Errorf("update: writing signal: %w", err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		return fmt.Errorf("update: finalizing signal: %w", err)
	}
	return nil
}

// Pending reports whether the watcher still has a request in flight. It removes
// requested only as the last step of a run, so "gone" means any result file now
// belongs to this request.
func Pending(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "requested"))
	return err == nil
}

// ReadResult reads the watcher's last outcome, or nil if none exists or it is
// not valid JSON (defensive; the script writes atomically).
func ReadResult(dir string) *Result {
	body, err := os.ReadFile(filepath.Join(dir, "result"))
	if err != nil {
		return nil
	}
	var res Result
	if err := json.Unmarshal(body, &res); err != nil {
		return nil
	}
	return &res
}

// Pending reports whether the host watcher still has a request in flight for
// this client's signal directory.
func (c *Client) Pending() bool { return Pending(c.SignalDir) }

// Result returns the host watcher's last recorded outcome for this client's
// signal directory, or nil.
func (c *Client) Result() *Result { return ReadResult(c.SignalDir) }

func newHTTPClient() *http.Client { return &http.Client{Timeout: requestTimeout} }

func readBounded(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("update: response exceeds %d bytes", maxResponseBytes)
	}
	return body, nil
}
