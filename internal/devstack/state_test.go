package devstack_test

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"github.com/AutumnsGrove/Ivy/internal/devstack"
	"github.com/AutumnsGrove/Ivy/internal/mailworld"
)

func newStateWorld(t *testing.T) *mailworld.World {
	t.Helper()
	w, err := mailworld.New()
	if err != nil {
		t.Fatalf("new world: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if _, err := mailworld.Seed(w, mailworld.Minimal(), mailworld.WithSeed(5)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return w
}

func dialIMAP(t *testing.T, w *mailworld.World) *imapclient.Client {
	t.Helper()
	c, err := imapclient.DialInsecure(w.IMAPAddr(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestStatesCoverDevDoc(t *testing.T) {
	t.Parallel()
	want := []string{
		"sync-auth-failed", "unreachable", "backfilling", "fetch-failed",
		"send-too-large", "send-transient-4xx", "llm-cap-reached",
		"llm-provider-down", "offline", "mirror-healthy",
	}
	got := map[string]bool{}
	for _, s := range devstack.States() {
		got[s.Name] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("state %q is missing", name)
		}
	}
}

func TestApplyStateUnknownFails(t *testing.T) {
	t.Parallel()
	if err := devstack.ApplyState(newStateWorld(t), "made-up"); err == nil {
		t.Fatal("expected an error for an unknown state")
	}
}

func TestApplyStateSyncAuthFailed(t *testing.T) {
	t.Parallel()
	w := newStateWorld(t)
	if err := devstack.ApplyState(w, "sync-auth-failed"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	c := dialIMAP(t, w)
	if err := c.Login("ivy@grove.test", mailworld.SeedPassword).Wait(); err == nil {
		t.Fatal("login succeeded under sync-auth-failed")
	}
}

func TestApplyStateUnreachable(t *testing.T) {
	t.Parallel()
	w := newStateWorld(t)
	if err := devstack.ApplyState(w, "unreachable"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Unreachable is a lasting condition: every retry must fail, not just the
	// first connection, or the sync-state screen would recover by itself.
	for attempt := 1; attempt <= 3; attempt++ {
		c, err := imapclient.DialInsecure(w.IMAPAddr(), nil)
		if err != nil {
			continue
		}
		loginErr := c.Login("ivy@grove.test", mailworld.SeedPassword).Wait()
		_ = c.Close()
		if loginErr == nil {
			t.Fatalf("login succeeded on attempt %d while unreachable", attempt)
		}
	}
}

func TestApplyStateFetchFailed(t *testing.T) {
	t.Parallel()
	w := newStateWorld(t)
	if err := devstack.ApplyState(w, "fetch-failed"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	c := dialIMAP(t, w)
	if err := c.Login("ivy@grove.test", mailworld.SeedPassword).Wait(); err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		t.Fatalf("select: %v", err)
	}
	if _, err := c.Fetch(imap.SeqSetNum(1), &imap.FetchOptions{UID: true}).Collect(); err == nil {
		t.Fatal("FETCH succeeded under fetch-failed")
	}
}

func TestApplyStateBackfillingSlowsResponses(t *testing.T) {
	t.Parallel()
	w := newStateWorld(t)
	if err := devstack.ApplyState(w, "backfilling"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	start := time.Now()
	if _, _, err := w.Account("ivy@grove.test", mailworld.SeedPassword).Status("INBOX"); err != nil {
		t.Fatalf("status: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
		t.Fatalf("responses were not slowed: %s", elapsed)
	}
}

func TestApplyStateSendTooLarge(t *testing.T) {
	t.Parallel()
	w := newStateWorld(t)
	if err := devstack.ApplyState(w, "send-too-large"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := smtpMail(w); err == nil {
		t.Fatal("SMTP mail succeeded under send-too-large")
	}
}

func TestApplyStateSendTransient4xx(t *testing.T) {
	t.Parallel()
	w := newStateWorld(t)
	if err := devstack.ApplyState(w, "send-transient-4xx"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := smtpMail(w); err == nil {
		t.Fatal("SMTP mail succeeded under send-transient-4xx")
	}
}

func TestApplyStateLLMProviderDown(t *testing.T) {
	t.Parallel()
	w := newStateWorld(t)
	if err := devstack.ApplyState(w, "llm-provider-down"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if code := chatStatus(t, w); code != http.StatusServiceUnavailable {
		t.Fatalf("chat status = %d, want 503", code)
	}
}

func TestApplyStateLLMCapReached(t *testing.T) {
	t.Parallel()
	w := newStateWorld(t)
	if err := devstack.ApplyState(w, "llm-cap-reached"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if code := chatStatus(t, w); code != http.StatusTooManyRequests {
		t.Fatalf("chat status = %d, want 429", code)
	}
}

func TestMirrorHealthyClearsPreviousState(t *testing.T) {
	t.Parallel()
	w := newStateWorld(t)
	if err := devstack.ApplyState(w, "send-too-large"); err != nil {
		t.Fatalf("apply send-too-large: %v", err)
	}
	if err := devstack.ApplyState(w, "mirror-healthy"); err != nil {
		t.Fatalf("apply mirror-healthy: %v", err)
	}
	if err := smtpMail(w); err != nil {
		t.Fatalf("SMTP still failing after mirror-healthy: %v", err)
	}
}

func smtpMail(w *mailworld.World) error {
	cl, err := smtp.Dial(w.SMTPAddr())
	if err != nil {
		return err
	}
	defer cl.Close()
	if err := cl.Auth(sasl.NewPlainClient("", "ivy@grove.test", mailworld.SeedPassword)); err != nil {
		return err
	}
	return cl.Mail("ivy@grove.test", nil)
}

func chatStatus(t *testing.T, w *mailworld.World) int {
	t.Helper()
	body := bytes.NewBufferString(`{"model":"x","messages":[{"role":"user","content":"hi"}]}`)
	resp, err := http.Post(w.OpenRouterURL()+"/chat/completions", "application/json", body)
	if err != nil {
		t.Fatalf("post chat: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}
