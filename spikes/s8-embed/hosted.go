package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// hostedMain checks an OpenRouter embedding model: output format, speed and cost on email-sized
// chunks, the long-context limit, and a small retrieval sanity check over the repo docs.
// usage: s8 hosted <docs-dir> <model> [<model> ...]   (key from ../../.env, never printed)
func hostedMain(docs string, models []string) {
	key := envKey()
	chunksText := chunks(docs, 1500)
	fmt.Printf("%d doc chunks, mean %d chars\n", len(chunksText), meanLen(chunksText))

	queries := []struct {
		q    string
		want []string
	}{
		{"how many days of backups do we keep", []string{"15 days", "backup"}},
		{"why can't we compile the Go binary on the board", []string{"swap", "cold", "S3"}},
		{"what does the needs_me question do", []string{"needs_me"}},
		{"how are tags stored on the mail server", []string{"keyword"}},
		{"which compression does Safari support", []string{"zstd"}},
		{"how big does the database get per message", []string{"KiB per message", "12 KiB", "0.4 MiB"}},
	}

	for _, model := range models {
		fmt.Printf("\n=== %s ===\n", model)
		var vecs [][]float32
		var tokens int
		var cost float64
		start := time.Now()
		for i := 0; i < len(chunksText); i += 32 {
			batch := chunksText[i:min(i+32, len(chunksText))]
			vs, usage, err := hostedEmbed(key, model, batch)
			if err != nil {
				fmt.Printf("batch at %d failed: %v\n", i, err)
				return
			}
			vecs = append(vecs, vs...)
			tokens += usage.PromptTokens
			cost += usage.Cost
		}
		el := time.Since(start)
		fmt.Printf("%d chunks, %d tokens (%.0f per chunk) in %.1fs => %.1f chunks/s, cost $%.6f ($%.2f per 100k such chunks)\n",
			len(vecs), tokens, float64(tokens)/float64(len(vecs)), el.Seconds(), float64(len(vecs))/el.Seconds(), cost, cost*100000/float64(len(vecs)))

		// Output format: dimensions, whether components are multiples of 1/128 (native int8), norm.
		v0 := vecs[0]
		allInt, maxAbs := true, 0.0
		for _, x := range v0 {
			if r := float64(x) * 128; math.Abs(r-math.Round(r)) > 1e-6 {
				allInt = false
			}
			maxAbs = math.Max(maxAbs, math.Abs(float64(x)))
		}
		var norm float64
		for _, x := range v0 {
			norm += float64(x) * float64(x)
		}
		fmt.Printf("dims=%d, every component a multiple of 1/128: %v, max |component| %.4f, L2 norm %.2f\n", len(v0), allInt, maxAbs, math.Sqrt(norm))

		// Determinism: the same text twice should give the same vector (embed-once relies on stability only
		// for new content, but a changing output would make re-embedding comparisons meaningless).
		again, _, _ := hostedEmbed(key, model, chunksText[:1])
		same := len(again) == 1
		if same {
			for i := range v0 {
				if v0[i] != again[0][i] {
					same = false
					break
				}
			}
		}
		fmt.Printf("same text twice gives an identical vector: %v\n", same)

		// Normalise copies for cosine.
		norms := make([][]float32, len(vecs))
		for i, v := range vecs {
			c := append([]float32(nil), v...)
			normalize(c)
			norms[i] = c
		}
		hits := 0
		for _, qq := range queries {
			qv, _, err := hostedEmbed(key, model, []string{qq.q})
			if err != nil {
				fmt.Println("query failed:", err)
				continue
			}
			q := append([]float32(nil), qv[0]...)
			normalize(q)
			type sc struct {
				i int
				s float32
			}
			var all []sc
			for i, v := range norms {
				all = append(all, sc{i, dotF(q, v)})
			}
			sort.Slice(all, func(a, b int) bool { return all[a].s > all[b].s })
			ok := false
			for _, r := range all[:3] {
				for _, w := range qq.want {
					if strings.Contains(strings.ToLower(chunksText[r.i]), strings.ToLower(w)) {
						ok = true
					}
				}
			}
			if ok {
				hits++
			}
			fmt.Printf("  top-3 holds the answer: %-5v  %q\n", ok, qq.q)
		}
		fmt.Printf("retrieval sanity: %d of %d queries have the answer in the top 3\n", hits, len(queries))

		// Long-context behaviour: about 10k tokens should work on a 32k model; about 37k should fail cleanly.
		long := strings.Repeat("The quarterly report covers hosting costs and renewal dates. ", 700) // ~40k chars
		_, u1, err1 := hostedEmbed(key, model, []string{long})
		fmt.Printf("%d-char input: err=%v tokens=%d\n", len(long), shortErr(err1), u1.PromptTokens)
		vlong := strings.Repeat("The quarterly report covers hosting costs and renewal dates. ", 2800) // ~165k chars
		_, u2, err2 := hostedEmbed(key, model, []string{vlong})
		fmt.Printf("%d-char input: err=%v tokens=%d\n", len(vlong), shortErr(err2), u2.PromptTokens)
	}
}

func shortErr(err error) string {
	if err == nil {
		return "none"
	}
	s := strings.Join(strings.Fields(err.Error()), " ")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

type hostedUsage struct {
	PromptTokens int     `json:"prompt_tokens"`
	Cost         float64 `json:"cost"`
}

func hostedEmbed(key, model string, input []string) ([][]float32, hostedUsage, error) {
	body, _ := json.Marshal(map[string]any{"model": model, "input": input})
	req, _ := http.NewRequest("POST", "https://openrouter.ai/api/v1/embeddings", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 90 * time.Second}).Do(req)
	if err != nil {
		return nil, hostedUsage{}, fmt.Errorf("%s", strings.ReplaceAll(err.Error(), key, "<key>"))
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var r struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Usage hostedUsage `json:"usage"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, hostedUsage{}, fmt.Errorf("status %d, unparsable body", resp.StatusCode)
	}
	if resp.StatusCode != 200 || r.Error.Message != "" {
		return nil, hostedUsage{}, fmt.Errorf("status %d: %s", resp.StatusCode, strings.ReplaceAll(r.Error.Message, key, "<key>"))
	}
	out := make([][]float32, len(r.Data))
	for i, d := range r.Data {
		out[i] = d.Embedding
	}
	return out, r.Usage, nil
}

func envKey() string {
	f, err := os.Open("../../.env")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok && k == "OPENROUTER_API_KEY" {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	panic("OPENROUTER_API_KEY missing")
}
