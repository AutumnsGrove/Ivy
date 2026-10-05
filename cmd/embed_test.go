package cmd

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/AutumnsGrove/Ivy/config"
)

// An account that asks for embeddings but lacks what they need (the API key, the
// Ollama address) was dropped without a word, so the operator saw a search that
// quietly never used meaning and no hint why.
func TestBuildEmbeddersSaysWhyAnAccountIsLeftOut(t *testing.T) {
	// Not parallel: it swaps the default logger and the environment.
	t.Setenv("OPENROUTER_API_KEY", "")
	var logged bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	cfg := &config.Config{
		Accounts: []config.Account{
			{ID: "hosted", EmbedProvider: "openrouter", LLMEnabled: true},
			{ID: "local", EmbedProvider: "ollama"},
		},
	}
	embedders, _ := buildEmbedders(cfg)
	if len(embedders) != 0 {
		t.Fatalf("embedders = %v, want none without a key or an Ollama address", embedders)
	}
	out := logged.String()
	for _, want := range []string{"hosted", "OPENROUTER_API_KEY", "local", "ollama_url"} {
		if !strings.Contains(out, want) {
			t.Errorf("log does not mention %q: %s", want, out)
		}
	}
}
