package mailworld

import (
	"encoding/json"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"
)

// DefaultEmbeddingDims matches the hosted default (pplx-embed-v1-0.6b) and the
// local Ollama default (nomic-embed-text is 768, so tests can override).
const DefaultEmbeddingDims = 1024

// The fake charges deterministic, non-zero costs so ledger tests have something
// to assert. Rates are per token, loosely modelled on the real providers.
const (
	jevCostPerToken    = 0.042 / 1_000_000
	chatCostPerToken   = 1.00 / 1_000_000
	visionCostPerToken = 3.00 / 1_000_000
	embedCostPerToken  = 0.02 / 1_000_000
)

// LLMCall is one request a fake provider received, kept for tests to assert on
// what Ivy actually sent (TESTING.md section 4).
type LLMCall struct {
	Provider string
	Endpoint string // systemone, chat, vision, embeddings, ollama_embeddings, ollama_embed
	Model    string
	Body     []byte
	Response []byte
	Time     time.Time
}

// JevAnswer is one answer in a fake systemone response.
type JevAnswer struct {
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
}

// OpenRouterURL is the fake OpenRouter base URL (already includes /api/v1).
func (w *World) OpenRouterURL() string { return w.openRouter.URL + "/api/v1" }

// OllamaURL is the fake Ollama base URL.
func (w *World) OllamaURL() string { return w.ollama.URL }

// SetEmbeddingDims changes the vector size the fake embedders return, so tests
// can model a provider whose model has a different size (Ollama's
// nomic-embed-text is 768).
func (w *World) SetEmbeddingDims(dims int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.embedDims = dims
}

// Calls returns every request the fake providers received, oldest first.
func (w *World) Calls() []LLMCall {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]LLMCall, len(w.calls))
	copy(out, w.calls)
	return out
}

// QueueChat makes the next chat or vision call return reply, so a test can pin
// the exact text (structured output, a cascade verdict, an error sentence).
func (w *World) QueueChat(reply string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.chatQueue = append(w.chatQueue, reply)
}

// QueueJev makes the next systemone call answer with exactly these answers.
func (w *World) QueueJev(answers map[string]JevAnswer) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.jevQueue = append(w.jevQueue, answers)
}

func (w *World) recordCall(c LLMCall) {
	w.mu.Lock()
	defer w.mu.Unlock()
	c.Time = w.clock.Now()
	w.calls = append(w.calls, c)
}

func (w *World) takeChat() (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.chatQueue) == 0 {
		return "", false
	}
	reply := w.chatQueue[0]
	w.chatQueue = w.chatQueue[1:]
	return reply, true
}

func (w *World) takeJev() (map[string]JevAnswer, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.jevQueue) == 0 {
		return nil, false
	}
	answers := w.jevQueue[0]
	w.jevQueue = w.jevQueue[1:]
	return answers, true
}

// failLLM answers an armed provider fault and reports whether it did. DEV.md
// section 4's llm-cap-reached and llm-provider-down map to the two statuses a
// real provider returns for those conditions.
func (w *World) failLLM(rw http.ResponseWriter) bool {
	switch {
	case w.hasFault(LLMDown{}):
		http.Error(rw, "provider unavailable", http.StatusServiceUnavailable)
		return true
	case w.hasFault(LLMCapReached{}):
		http.Error(rw, "spend cap reached", http.StatusTooManyRequests)
		return true
	default:
		return false
	}
}

func (w *World) openRouterMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/systemone", w.handleSystemone)
	mux.HandleFunc("POST /api/v1/chat/completions", w.handleChat)
	mux.HandleFunc("POST /api/v1/vision", w.handleVision)
	mux.HandleFunc("POST /api/v1/embeddings", w.handleOpenRouterEmbeddings)
	return mux
}

func (w *World) ollamaMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/embeddings", w.handleOllamaEmbeddings)
	mux.HandleFunc("POST /api/embed", w.handleOllamaEmbed)
	return mux
}

type jevQuestion struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria"`
}

type llmUsage struct {
	PromptTokens int     `json:"prompt_tokens"`
	OutputTokens int     `json:"output_tokens"`
	TotalTokens  int     `json:"total_tokens"`
	Cost         float64 `json:"cost"`
}

// jevUsage matches the real /systemone usage shape.
type jevUsage struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	Cost         float64 `json:"cost"`
}

func (w *World) handleSystemone(rw http.ResponseWriter, r *http.Request) {
	if w.failLLM(rw) {
		return
	}
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Model     string                 `json:"model"`
		State     json.RawMessage        `json:"state"`
		Questions map[string]jevQuestion `json:"questions"`
	}
	_ = json.Unmarshal(body, &req)

	state := flattenState(req.State)
	answers := make(map[string]JevAnswer, len(req.Questions))
	if queued, ok := w.takeJev(); ok {
		for id, a := range queued {
			answers[id] = a
		}
	} else {
		for id, q := range req.Questions {
			answers[id] = jevChoose(id, q, state)
		}
	}
	tokens := estimateTokens(state)
	for _, q := range req.Questions {
		tokens += estimateTokens(q.Instructions)
	}
	resp := struct {
		Answers map[string]JevAnswer `json:"answers"`
		Usage   jevUsage             `json:"usage"`
	}{
		Answers: answers,
		Usage:   jevUsage{InputTokens: tokens, Cost: float64(tokens) * jevCostPerToken},
	}
	out := writeJSON(rw, resp)
	w.recordCall(LLMCall{Provider: "openrouter", Endpoint: "systemone", Model: req.Model, Body: body, Response: out})
}

func (w *World) handleChat(rw http.ResponseWriter, r *http.Request)   { w.serveChat(rw, r, false) }
func (w *World) handleVision(rw http.ResponseWriter, r *http.Request) { w.serveChat(rw, r, true) }

func (w *World) serveChat(rw http.ResponseWriter, r *http.Request, forceVision bool) {
	if w.failLLM(rw) {
		return
	}
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(body, &req)

	var prompt strings.Builder
	vision := forceVision
	for _, m := range req.Messages {
		text, hasImage := chatParts(m.Content)
		if hasImage {
			vision = true
		}
		if text != "" {
			prompt.WriteString(text)
			prompt.WriteString("\n")
		}
	}

	reply, ok := w.takeChat()
	if !ok {
		reply = fakeReply(prompt.String(), vision)
	}
	endpoint := "chat"
	rate := chatCostPerToken
	if vision {
		endpoint = "vision"
		rate = visionCostPerToken
	}
	resp := newChatResponse(req.Model, reply, estimateTokens(prompt.String())+1, rate)
	out := writeJSON(rw, resp)
	w.recordCall(LLMCall{Provider: "openrouter", Endpoint: endpoint, Model: req.Model, Body: body, Response: out})
}

type chatMessageOut struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatChoice struct {
	Index        int            `json:"index"`
	Message      chatMessageOut `json:"message"`
	FinishReason string         `json:"finish_reason"`
}

type chatCompletion struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
	Usage   llmUsage     `json:"usage"`
}

func newChatResponse(model, reply string, promptTokens int, rate float64) chatCompletion {
	outTokens := estimateTokens(reply)
	return chatCompletion{
		ID:     "chatcmpl-fake",
		Object: "chat.completion",
		Model:  model,
		Choices: []chatChoice{{
			Message:      chatMessageOut{Role: "assistant", Content: reply},
			FinishReason: "stop",
		}},
		Usage: llmUsage{
			PromptTokens: promptTokens,
			OutputTokens: outTokens,
			TotalTokens:  promptTokens + outTokens,
			Cost:         float64(promptTokens+outTokens) * rate,
		},
	}
}

func (w *World) handleOpenRouterEmbeddings(rw http.ResponseWriter, r *http.Request) {
	if w.failLLM(rw) {
		return
	}
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Model string          `json:"model"`
		Input json.RawMessage `json:"input"`
	}
	_ = json.Unmarshal(body, &req)

	inputs := parseInputs(req.Input)
	resp := embeddingsResponse{Object: "list", Model: req.Model, Data: make([]embeddingData, len(inputs))}
	tokens := 0
	for i, text := range inputs {
		resp.Data[i] = embeddingData{Object: "embedding", Index: i, Embedding: w.embed(text)}
		tokens += estimateTokens(text)
	}
	resp.Usage = llmUsage{PromptTokens: tokens, TotalTokens: tokens, Cost: float64(tokens) * embedCostPerToken}
	out := writeJSON(rw, resp)
	w.recordCall(LLMCall{Provider: "openrouter", Endpoint: "embeddings", Model: req.Model, Body: body, Response: out})
}

type embeddingData struct {
	Object    string    `json:"object"`
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

type embeddingsResponse struct {
	Object string          `json:"object"`
	Data   []embeddingData `json:"data"`
	Model  string          `json:"model"`
	Usage  llmUsage        `json:"usage"`
}

func (w *World) handleOllamaEmbeddings(rw http.ResponseWriter, r *http.Request) {
	if w.failLLM(rw) {
		return
	}
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Model  string `json:"model"`
		Prompt string `json:"prompt"`
	}
	_ = json.Unmarshal(body, &req)
	resp := struct {
		Embedding []float32 `json:"embedding"`
	}{Embedding: w.embed(req.Prompt)}
	out := writeJSON(rw, resp)
	w.recordCall(LLMCall{Provider: "ollama", Endpoint: "ollama_embeddings", Model: req.Model, Body: body, Response: out})
}

func (w *World) handleOllamaEmbed(rw http.ResponseWriter, r *http.Request) {
	if w.failLLM(rw) {
		return
	}
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Model string          `json:"model"`
		Input json.RawMessage `json:"input"`
	}
	_ = json.Unmarshal(body, &req)
	inputs := parseInputs(req.Input)
	resp := struct {
		Model      string      `json:"model"`
		Embeddings [][]float32 `json:"embeddings"`
		PromptEval int         `json:"prompt_eval_count"`
	}{Model: req.Model, Embeddings: make([][]float32, len(inputs))}
	for i, text := range inputs {
		resp.Embeddings[i] = w.embed(text)
		resp.PromptEval += estimateTokens(text)
	}
	out := writeJSON(rw, resp)
	w.recordCall(LLMCall{Provider: "ollama", Endpoint: "ollama_embed", Model: req.Model, Body: body, Response: out})
}

// embed is a hashing bag-of-words vectoriser: deterministic, and texts that
// share words score higher, so fake retrieval tests behave sensibly.
func (w *World) embed(text string) []float32 {
	w.mu.Lock()
	dims := w.embedDims
	w.mu.Unlock()
	if dims <= 0 {
		dims = DefaultEmbeddingDims
	}
	v := make([]float32, dims)
	for _, tok := range tokenize(text) {
		h := hash64(tok)
		idx := h % uint64(dims) // any integer type may index a slice
		if h&(1<<63) != 0 {
			v[idx]--
		} else {
			v[idx]++
		}
	}
	var norm float64
	for _, x := range v {
		norm += float64(x) * float64(x)
	}
	if norm > 0 {
		n := float32(math.Sqrt(norm))
		for i := range v {
			v[i] /= n
		}
	}
	return v
}

func chatParts(raw json.RawMessage) (text string, hasImage bool) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, false
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Type == "image_url" || p.Type == "image" {
				hasImage = true
			}
			if p.Text != "" {
				b.WriteString(p.Text)
				b.WriteString(" ")
			}
		}
		return strings.TrimSpace(b.String()), hasImage
	}
	return "", false
}

func parseInputs(raw json.RawMessage) []string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []string{s}
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	return nil
}

func flattenState(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(p.Text)
			b.WriteString("\n")
		}
		return b.String()
	}
	return string(raw)
}

func fakeReply(prompt string, vision bool) string {
	words := strings.Fields(prompt)
	if len(words) > 12 {
		words = words[:12]
	}
	prefix := "Fake reply: "
	if vision {
		prefix = "Fake vision: "
	}
	return prefix + strings.Join(words, " ")
}

// jevQuiet names each known question's do-nothing option, so a state with no
// signal answers quietly instead of picking an arbitrary option.
var jevQuiet = map[string]string{
	"needs_me":             "none",
	"category":             "other",
	"is_automated":         "no",
	"urgency":              "normal",
	"has_deadline":         "no",
	"asks_question":        "no",
	"receipt_or_invoice":   "no",
	"phishing_risk":        "none",
	"injection_tripwire":   "no",
	"contact_form_quality": "genuine",
	"cold_outreach":        "no",
	"junk_rescue":          "junk",
	"thread_state":         "open",
	"worth_summarizing":    "no",
	"digest_worthy":        "skip",
	"snooze_suggest":       "none",
}

// jevKeywords maps a question id and option to words that should make it fire.
// It is deliberately simple: enough for the seeded dev loop and deterministic
// tests, not a claim about real accuracy.
var jevKeywords = map[string]map[string][]string{
	"needs_me": {
		"likely": {"?", "please ", "confirm", "deadline", "urgent", "reply", "rsvp", "action required", "needs your"},
		"maybe":  {"let me know", "can you", "could you", "would you", "when you get a chance"},
	},
	"category": {
		"receipt":         {"receipt", "invoice", "payment", "renewal", "billing", "subscription", "order"},
		"newsletter":      {"unsubscribe", "newsletter", "digest", "issue #", "weekly"},
		"contact_form":    {"contact form", "website enquiry", "enquiry", "submitted the form"},
		"security_report": {"security", "abuse", "vulnerability", "responsible disclosure"},
		"personal":        {"lunch", "dinner", "coffee", "friday", "weekend", "let me know", "free for", "see you", "catch up"},
		"notification":    {"notification", "alert", "verify", "code", "sign-in"},
	},
	"is_automated":       {"yes": {"unsubscribe", "no-reply", "noreply", "do not reply", "automated"}},
	"urgency":            {"high": {"urgent", "today", "asap", "immediately", "expires"}},
	"has_deadline":       {"yes": {"deadline", "due", "expires", "by friday", "before"}},
	"asks_question":      {"yes": {"?", "please", "can you", "could you"}},
	"receipt_or_invoice": {"receipt": {"receipt"}, "invoice": {"invoice"}, "renewal_notice": {"renewal"}, "payment_failed": {"payment failed", "declined"}},
	"phishing_risk":      {"likely": {"verify your account", "click here to claim", "suspended", "unusual sign-in"}, "suspicious": {"urgent action", "confirm your password"}},
	"injection_tripwire": {"yes": {"ignore previous", "system prompt", "as an ai", "you are an assistant", "disregard"}},
}

func jevChoose(id string, q jevQuestion, state string) JevAnswer {
	options := make([]string, 0, len(q.Criteria))
	for k := range q.Criteria {
		options = append(options, k)
	}
	sort.Strings(options)
	if len(options) == 0 {
		return JevAnswer{}
	}

	text := strings.ToLower(state)
	best, bestScore := "", -1
	for _, opt := range options {
		score := 0
		for _, kw := range jevKeywords[id][opt] {
			if strings.Contains(text, kw) {
				score++
			}
		}
		if score > bestScore {
			best, bestScore = opt, score
		}
	}
	if bestScore <= 0 {
		best = quietOption(id, options)
	}
	return jevAnswer(best, options, state+id)
}

func quietOption(id string, options []string) string {
	if quiet, ok := jevQuiet[id]; ok {
		for _, o := range options {
			if o == quiet {
				return o
			}
		}
	}
	return options[0]
}

func jevAnswer(choice string, options []string, seed string) JevAnswer {
	if len(options) == 1 {
		return JevAnswer{Choice: choice, Probabilities: map[string]float64{choice: 1}, Confidence: 1}
	}
	h := hash64(seed)
	winner := 0.70 + float64(h%2000)/10000.0
	probs := make(map[string]float64, len(options))
	probs[choice] = winner
	rest := (1 - winner) / float64(len(options)-1)
	for _, o := range options {
		if o == choice {
			continue
		}
		probs[o] = rest + float64(hash64(seed+o)%100)/10000.0
	}
	var total float64
	for _, o := range options {
		total += probs[o]
	}
	for _, o := range options {
		probs[o] /= total
	}
	conf := 0.75 + float64((h>>8)%2300)/10000.0
	return JevAnswer{Choice: choice, Probabilities: probs, Confidence: conf}
}

func estimateTokens(s string) int {
	if s == "" {
		return 1
	}
	return len(s)/4 + 1
}

func tokenize(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func hash64(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

func writeJSON(rw http.ResponseWriter, v any) []byte {
	out, err := json.Marshal(v)
	if err != nil {
		http.Error(rw, `{"error":"fake provider marshal failed"}`, http.StatusInternalServerError)
		return nil
	}
	rw.Header().Set("Content-Type", "application/json")
	_, _ = rw.Write(out)
	return out
}
