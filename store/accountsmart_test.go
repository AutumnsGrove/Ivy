package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func smartConfig(id string, on bool) AccountConfig {
	return AccountConfig{
		ID: id, Address: id + "@example.test", Username: id,
		IMAPHost: "imap.example.test", IMAPPort: 993, SMTPHost: "smtp.example.test", SMTPPort: 465,
		LLMEnabled: on, CreatedAt: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC),
	}
}

// The smart-features switch lives in state.db with the rest of the operator's
// choices, so it survives a restart and a mirror rebuild.
func TestSetAccountSmartPersistsAndNamesTheProvider(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	if err := dbs.SaveAccountConfig(ctx, smartConfig("a1", false)); err != nil {
		t.Fatal(err)
	}

	if err := dbs.SetAccountSmart(ctx, "a1", true); err != nil {
		t.Fatalf("turn on: %v", err)
	}
	rows, _ := dbs.AccountConfigs(ctx)
	if len(rows) != 1 || !rows[0].LLMEnabled || rows[0].EmbedProvider != "openrouter" {
		t.Errorf("after on = %+v, want enabled with the openrouter provider (as at connect)", rows)
	}

	if err := dbs.SetAccountSmart(ctx, "a1", false); err != nil {
		t.Fatalf("turn off: %v", err)
	}
	rows, _ = dbs.AccountConfigs(ctx)
	if rows[0].LLMEnabled || rows[0].EmbedProvider != "openrouter" {
		t.Errorf("after off = %+v, want disabled and the provider kept for turning it back on", rows[0])
	}
}

func TestSetAccountSmartRefusesAnAccountWithNoStoredConfig(t *testing.T) {
	t.Parallel()
	dbs := openTemp(t)
	if err := dbs.SetAccountSmart(context.Background(), "from-ivy-yaml", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound (an ivy.yaml account is not changed from the app)", err)
	}
}
