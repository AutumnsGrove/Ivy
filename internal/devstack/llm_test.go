package devstack

import (
	"strings"
	"testing"
)

func TestResolveLLM(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		want       LLM
		env, dot   string
		mode       LLM
		key        string
		noteHas    string
		keyIsFake  bool
		wantNoNote bool
	}{
		{name: "fake asked for", want: LLMFake, env: "real", mode: LLMFake, keyIsFake: true, wantNoNote: true},
		{name: "live with a key in the environment", want: LLMLive, env: "env-key", mode: LLMLive, key: "env-key", wantNoNote: true},
		{name: "live with a key only in .env", want: LLMLive, dot: "dot-key", mode: LLMLive, key: "dot-key", wantNoNote: true},
		{name: "the environment wins over .env", want: LLMLive, env: "env-key", dot: "dot-key", mode: LLMLive, key: "env-key", wantNoNote: true},
		{name: "live with no key falls back to the fake, loudly", want: LLMLive, mode: LLMFake, keyIsFake: true, noteHas: "OPENROUTER_API_KEY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mode, key, note := ResolveLLM(tc.want, tc.env, tc.dot)
			if mode != tc.mode {
				t.Errorf("mode = %q, want %q", mode, tc.mode)
			}
			if tc.keyIsFake && key != FakeKey {
				t.Errorf("key = %q, want the throwaway fake key (a real key must never reach the fake endpoint)", key)
			}
			if !tc.keyIsFake && key != tc.key {
				t.Errorf("key = %q, want %q", key, tc.key)
			}
			if tc.wantNoNote && note != "" {
				t.Errorf("note = %q, want none", note)
			}
			if tc.noteHas != "" && !strings.Contains(note, tc.noteHas) {
				t.Errorf("note = %q, want it to mention %s", note, tc.noteHas)
			}
		})
	}
}
