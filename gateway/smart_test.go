package gateway

import (
	"context"
	"net/http"
	"testing"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/store"
)

// Issue #8: sync creates the mirror row without llm_enabled, so `smart` read off
// that row said false for every real account. The operator's choice lives in
// state.db (an app-connected account) or ivy.yaml, and that is what the API
// reports, so the mirror can be rebuilt without losing or inventing it.
func TestAccountsReportSmartFromTheConfiguredAccount(t *testing.T) {
	t.Parallel()
	srv, dbs := newConfiguredServer(t, func(s *Server) {
		s.WithConfiguredSmart(map[string]bool{"yaml-on": true, "yaml-off": false})
	})
	// The mirror rows are what sync creates: no llm_enabled at all, except one
	// stale copy that says on for an account the config now turns off.
	mustAccount(t, dbs, store.Account{ID: "app-on", Address: "a@example.com", SortOrder: 0})
	mustAccount(t, dbs, store.Account{ID: "app-off", Address: "b@example.com", SortOrder: 1})
	mustAccount(t, dbs, store.Account{ID: "yaml-on", Address: "c@example.com", SortOrder: 2})
	mustAccount(t, dbs, store.Account{ID: "yaml-off", Address: "d@example.com", SortOrder: 3, LLMEnabled: true})
	for id, smart := range map[string]bool{"app-on": true, "app-off": false} {
		err := dbs.SaveAccountConfig(context.Background(), store.AccountConfig{
			ID: id, Address: id + "@example.com", Username: id,
			IMAPHost: "imap.example.com", IMAPPort: 993, SMTPHost: "smtp.example.com", SMTPPort: 465,
			LLMEnabled: smart, CreatedAt: testNow,
		})
		if err != nil {
			t.Fatalf("SaveAccountConfig(%s): %v", id, err)
		}
	}

	var accounts []api.Account
	if code := getJSON(t, srv.URL+"/api/v1/accounts", &accounts); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	got := map[string]bool{}
	for _, a := range accounts {
		got[a.Id] = a.Smart
	}
	want := map[string]bool{"app-on": true, "app-off": false, "yaml-on": true, "yaml-off": false}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("smart for %s = %v, want %v", id, got[id], w)
		}
	}
}
