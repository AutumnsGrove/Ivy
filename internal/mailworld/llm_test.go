package mailworld_test

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
)

// postJSON sends an application/json request and returns status and body. The
// transport disables keep-alives so no client goroutine outlives the test
// (goleak is on for this package).
func postJSON(t *testing.T, url string, body any) (int, []byte) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	resp, err := testHTTP.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("post %s: %v", url, err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return resp.StatusCode, out
}

var testHTTP = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

func TestFakeJevAnswersEveryQuestionDeterministically(t *testing.T) {
	t.Parallel()
	w := newWorld(t)

	req := map[string]any{
		"model": "jev-latest",
		"state": "From: a@example.com\nSubject: Lunch?\n\nAre you free for lunch on Friday? Let me know.",
		"questions": map[string]any{
			"needs_me": map[string]any{
				"type":         "choice",
				"instructions": "Does this need the recipient to reply?",
				"criteria":     map[string]string{"none": "nothing is asked", "maybe": "unclear", "likely": "clearly needs a reply"},
			},
			"category": map[string]any{
				"type":         "choice",
				"instructions": "What kind of email is this?",
				"criteria":     map[string]string{"personal": "a person", "newsletter": "a list", "other": "none of the above"},
			},
		},
	}
	status, body := postJSON(t, w.OpenRouterURL()+"/systemone", req)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	var got struct {
		Answers map[string]mailworld.JevAnswer `json:"answers"`
		Usage   struct {
			InputTokens int     `json:"input_tokens"`
			Cost        float64 `json:"cost"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, body)
	}
	if len(got.Answers) != 2 {
		t.Fatalf("got %d answers, want 2", len(got.Answers))
	}
	nm := got.Answers["needs_me"]
	if nm.Choice == "none" {
		t.Errorf("needs_me = none for a question email, want it to fire")
	}
	if len(nm.Probabilities) != 3 {
		t.Errorf("needs_me probabilities = %v, want all three options", nm.Probabilities)
	}
	if math.Abs(sumProbs(nm.Probabilities)-1) > 0.001 {
		t.Errorf("probabilities sum to %v, want 1", sumProbs(nm.Probabilities))
	}
	if got.Answers["category"].Choice != "personal" {
		t.Errorf("category = %q, want personal", got.Answers["category"].Choice)
	}
	if got.Usage.InputTokens == 0 || got.Usage.Cost <= 0 {
		t.Errorf("usage = %+v, want positive tokens and cost", got.Usage)
	}

	// The same request must get the same answer, or seeded tests cannot assert.
	_, again := postJSON(t, w.OpenRouterURL()+"/systemone", req)
	if string(again) != string(body) {
		t.Errorf("second response differs:\n%s\n%s", body, again)
	}
}

func TestFakeJevReadsStateArray(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	req := map[string]any{
		"model": "jev-latest",
		"state": []map[string]string{{"source": "headers", "text": "Subject: Weekly digest"}},
		"questions": map[string]any{
			"category": map[string]any{
				"type":         "choice",
				"instructions": "What kind?",
				"criteria":     map[string]string{"personal": "a person", "newsletter": "a list", "other": "else"},
			},
		},
	}
	status, body := postJSON(t, w.OpenRouterURL()+"/systemone", req)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	var got struct {
		Answers map[string]mailworld.JevAnswer `json:"answers"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Answers["category"].Choice != "newsletter" {
		t.Errorf("category = %q, want newsletter", got.Answers["category"].Choice)
	}
}

func TestFakeJevQueueOverrides(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	want := mailworld.JevAnswer{Choice: "likely", Probabilities: map[string]float64{"likely": 0.99, "none": 0.01}, Confidence: 0.99}
	w.QueueJev(map[string]mailworld.JevAnswer{"needs_me": want})

	req := map[string]any{
		"model": "jev-latest",
		"state": "anything",
		"questions": map[string]any{
			"needs_me": map[string]any{
				"type":         "choice",
				"instructions": "x",
				"criteria":     map[string]string{"none": "no", "likely": "yes"},
			},
		},
	}
	_, body := postJSON(t, w.OpenRouterURL()+"/systemone", req)
	var got struct {
		Answers map[string]mailworld.JevAnswer `json:"answers"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Answers["needs_me"].Choice != "likely" || got.Answers["needs_me"].Confidence != 0.99 {
		t.Errorf("queued answer not used: %+v", got.Answers["needs_me"])
	}
}

func TestFakeChatAndVision(t *testing.T) {
	t.Parallel()
	w := newWorld(t)

	chatReq := map[string]any{
		"model":    "some/chat",
		"messages": []map[string]any{{"role": "user", "content": "Summarise the thread please."}},
	}
	status, body := postJSON(t, w.OpenRouterURL()+"/chat/completions", chatReq)
	if status != http.StatusOK {
		t.Fatalf("chat status = %d, body %s", status, body)
	}
	reply := chatContent(t, body)
	if reply == "" {
		t.Fatalf("empty chat reply: %s", body)
	}
	_, again := postJSON(t, w.OpenRouterURL()+"/chat/completions", chatReq)
	if chatContent(t, again) != reply {
		t.Errorf("chat reply is not deterministic")
	}

	// A vision call carries an image content part; it must be logged separately.
	visionReq := map[string]any{
		"model": "some/vision",
		"messages": []map[string]any{{"role": "user", "content": []map[string]any{
			{"type": "text", "text": "What is in this receipt?"},
			{"type": "image_url", "image_url": map[string]string{"url": "data:image/png;base64,AAAA"}},
		}}},
	}
	status, body = postJSON(t, w.OpenRouterURL()+"/chat/completions", visionReq)
	if status != http.StatusOK {
		t.Fatalf("vision status = %d, body %s", status, body)
	}
	var sawVision bool
	for _, c := range w.Calls() {
		if c.Endpoint == "vision" {
			sawVision = true
		}
	}
	if !sawVision {
		t.Errorf("vision call not logged as vision: %+v", w.Calls())
	}

	// The explicit vision alias works too.
	status, _ = postJSON(t, w.OpenRouterURL()+"/vision", visionReq)
	if status != http.StatusOK {
		t.Fatalf("alias status = %d", status)
	}
}

func TestFakeChatQueue(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.QueueChat("exact reply")

	status, body := postJSON(t, w.OpenRouterURL()+"/chat/completions", map[string]any{
		"model":    "m",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	if got := chatContent(t, body); got != "exact reply" {
		t.Errorf("reply = %q, want exact reply", got)
	}
}

func TestFakeOpenRouterEmbeddings(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	url := w.OpenRouterURL() + "/embeddings"

	status, body := postJSON(t, url, map[string]any{"model": "perplexity/pplx-embed-v1-0.6b", "input": []string{"renewal notice for the domain"}})
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	var got struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Usage struct {
			PromptTokens int     `json:"prompt_tokens"`
			Cost         float64 `json:"cost"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Data) != 1 || len(got.Data[0].Embedding) != mailworld.DefaultEmbeddingDims {
		t.Fatalf("got %d vectors of dim %d, want 1 of %d", len(got.Data), len(got.Data[0].Embedding), mailworld.DefaultEmbeddingDims)
	}
	if got.Usage.PromptTokens <= 0 || got.Usage.Cost <= 0 {
		t.Errorf("usage = %+v, want positive tokens and cost", got.Usage)
	}

	// Deterministic, and hashing-based so shared words score higher.
	same := embedOne(t, url, "renewal notice for the domain")
	other := embedOne(t, url, "renewal notice for the domain expires")
	unrelated := embedOne(t, url, "quarterly budget spreadsheet")
	if !equalVec(same, got.Data[0].Embedding) {
		t.Errorf("same text gave a different vector")
	}
	if cosine(same, unrelated) >= cosine(same, other) {
		t.Errorf("related text should score higher than unrelated")
	}
}

func TestFakeEmbeddingDimsCanBeSet(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.SetEmbeddingDims(768)
	if got := len(embedOne(t, w.OpenRouterURL()+"/embeddings", "x")); got != 768 {
		t.Errorf("dims = %d, want 768", got)
	}
}

func TestFakeOllamaEmbeddings(t *testing.T) {
	t.Parallel()
	w := newWorld(t)

	status, body := postJSON(t, w.OllamaURL()+"/api/embeddings", map[string]any{"model": "nomic-embed-text", "prompt": "hello world"})
	if status != http.StatusOK {
		t.Fatalf("legacy status = %d, body %s", status, body)
	}
	var legacy struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := json.Unmarshal(body, &legacy); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}
	if len(legacy.Embedding) != mailworld.DefaultEmbeddingDims {
		t.Fatalf("legacy dim = %d, want %d", len(legacy.Embedding), mailworld.DefaultEmbeddingDims)
	}

	status, body = postJSON(t, w.OllamaURL()+"/api/embed", map[string]any{"model": "nomic-embed-text", "input": []string{"hello world", "bye"}})
	if status != http.StatusOK {
		t.Fatalf("new status = %d, body %s", status, body)
	}
	var modern struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.Unmarshal(body, &modern); err != nil {
		t.Fatalf("unmarshal modern: %v", err)
	}
	if len(modern.Embeddings) != 2 {
		t.Fatalf("got %d embeddings, want 2", len(modern.Embeddings))
	}
	if !equalVec(modern.Embeddings[0], legacy.Embedding) {
		t.Errorf("the same text should embed the same on both Ollama endpoints")
	}
}

func TestFakeProviderCallLog(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	postJSON(t, w.OpenRouterURL()+"/chat/completions", map[string]any{
		"model":    "m1",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	calls := w.Calls()
	if len(calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(calls))
	}
	if calls[0].Provider != "openrouter" || calls[0].Endpoint != "chat" || calls[0].Model != "m1" {
		t.Errorf("call = %+v, want openrouter/chat/m1", calls[0])
	}
	if !bytes.Contains(calls[0].Body, []byte("hi")) {
		t.Errorf("logged body does not contain the request: %s", calls[0].Body)
	}
	if len(calls[0].Response) == 0 {
		t.Errorf("logged response is empty")
	}
}

func TestFakeProvidersAreLoopbackOnly(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	for name, raw := range map[string]string{"openrouter": w.OpenRouterURL(), "ollama": w.OllamaURL()} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("%s: parse %q: %v", name, raw, err)
		}
		ip := net.ParseIP(u.Hostname())
		if ip == nil || !ip.IsLoopback() {
			t.Errorf("%s url %q is not loopback", name, raw)
		}
	}
}

// TestNoTestReachesLiveProvider is the guard from TESTING.md section 4: no test
// may hardcode a live provider host, so the fake is the only path tests have.
func TestNoTestReachesLiveProvider(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "web", "docs", "spikes", ".dev":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, host := range []string{"openrouter" + ".ai", "api." + "openai.com", "api." + "anthropic.com"} {
			if bytes.Contains(b, []byte(host)) {
				t.Errorf("%s hardcodes the live provider %q; use the mailworld fake", path, host)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo: %v", err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above test directory")
		}
		dir = parent
	}
}

func sumProbs(p map[string]float64) float64 {
	var s float64
	for _, v := range p {
		s += v
	}
	return s
}

func chatContent(t *testing.T, body []byte) string {
	t.Helper()
	var r struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatalf("unmarshal chat: %v (%s)", err, body)
	}
	if len(r.Choices) == 0 {
		return ""
	}
	return r.Choices[0].Message.Content
}

func embedOne(t *testing.T, url, text string) []float32 {
	t.Helper()
	status, body := postJSON(t, url, map[string]any{"model": "m", "input": []string{text}})
	if status != http.StatusOK {
		t.Fatalf("embed status = %d, body %s", status, body)
	}
	var r struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(r.Data) != 1 {
		t.Fatalf("got %d embeddings, want 1", len(r.Data))
	}
	return r.Data[0].Embedding
}

func equalVec(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
