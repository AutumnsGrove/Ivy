// Package search turns mail into a hybrid keyword and meaning index: it chunks
// text for embeddings, fuses FTS5 and vector results, and runs the embed-once
// background queue. The FTS queries themselves live in store (ARCHITECTURE.md 6).
package search

import "strings"

// Chunking is by token count with margin, not by characters
// (ARCHITECTURE.md 3). Ivy has no provider tokenizer, so it uses a deliberate
// over-estimate: 4 bytes per token, which keeps a chunk under the model
// context even for dense text.
const (
	ChunkTokens  = 512 // approximate tokens per chunk
	ChunkOverlap = 64  // approximate tokens shared with the next chunk
	BytesPerToken = 4
	// MaxChunks bounds one document so a huge body cannot cost an unbounded
	// embedding bill (STANDARDS.md 4a).
	MaxChunks = 64
)

// Chunk splits text into overlapping pieces of at most maxTokens approximate
// tokens, on word boundaries. An empty or whitespace-only text yields no
// chunks. It always makes progress, so a single very long word is cut.
func Chunk(text string, maxTokens, overlapTokens int) []string {
	if maxTokens <= 0 {
		maxTokens = ChunkTokens
	}
	if overlapTokens < 0 || overlapTokens >= maxTokens {
		overlapTokens = ChunkOverlap
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	maxBytes := maxTokens * BytesPerToken
	overlapBytes := overlapTokens * BytesPerToken

	var chunks []string
	var cur []string
	size := 0
	flush := func() {
		if len(cur) > 0 {
			chunks = append(chunks, strings.Join(cur, " "))
		}
	}
	for _, w := range words {
		wlen := len(w) + 1
		if size+wlen > maxBytes && len(cur) > 0 {
			flush()
			if len(chunks) >= MaxChunks {
				return chunks
			}
			// Start the next chunk with the tail of the previous one, so a
			// sentence split across a boundary is still in one chunk.
			cur = overlapTail(cur, overlapBytes)
			size = 0
			for _, t := range cur {
				size += len(t) + 1
			}
		}
		// A word longer than the whole budget is cut to fit rather than
		// skipped, so its text is still searchable.
		if wlen > maxBytes {
			w = w[:maxBytes-1]
			wlen = len(w) + 1
		}
		cur = append(cur, w)
		size += wlen
	}
	if len(chunks) < MaxChunks {
		flush()
	}
	return chunks
}

// overlapTail returns the trailing words of cur whose total size is at most
// overlapBytes, in order.
func overlapTail(cur []string, overlapBytes int) []string {
	if overlapBytes <= 0 {
		return nil
	}
	total := 0
	start := len(cur)
	for start > 0 {
		next := len(cur[start-1]) + 1
		if total+next > overlapBytes {
			break
		}
		total += next
		start--
	}
	out := make([]string, len(cur)-start)
	copy(out, cur[start:])
	return out
}

// DocChunks builds the chunks for one message body: the subject joined to the
// body, then the body split. The subject leads so a short body and its subject
// share a chunk, which is what a search for the subject should hit.
func DocChunks(subject, body string) []string {
	text := strings.TrimSpace(subject)
	if b := strings.TrimSpace(body); b != "" {
		if text != "" {
			text += "\n"
		}
		text += b
	}
	return Chunk(text, ChunkTokens, ChunkOverlap)
}
