package thread

import (
	"fmt"
	"strconv"
	"testing"
	"time"
)

// BenchmarkBuild models an account the sync pass re-threads after every run: a
// deep reply chain (the recursion-heavy shape) and a flat pile of unrelated
// mail (the subject-grouping map). Numbers come from the potato, not here;
// this keeps the shape and catches a regression in CI's advisory bench job.
func BenchmarkBuild(b *testing.B) {
	sizes := []int{100, 1_000, 10_000}
	b.Run("chain", func(b *testing.B) {
		for _, n := range sizes {
			b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
				msgs := chain(n)
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					Build(msgs)
				}
			})
		}
	})
	b.Run("flat", func(b *testing.B) {
		for _, n := range sizes {
			b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
				msgs := flat(n)
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					Build(msgs)
				}
			})
		}
	})
}

func chain(n int) []Message {
	msgs := make([]Message, n)
	prev := ""
	for i := 0; i < n; i++ {
		id := "m" + strconv.Itoa(i)
		m := Message{
			ID: id, Key: "k" + id, MessageID: "<" + id + "@x>",
			Subject: "Re: Topic", Date: base.Add(time.Duration(i) * time.Minute),
		}
		if prev != "" {
			m.References, m.InReplyTo = prev, prev
		}
		prev = "<" + id + "@x>"
		msgs[i] = m
	}
	return msgs
}

func flat(n int) []Message {
	msgs := make([]Message, n)
	for i := 0; i < n; i++ {
		id := "m" + strconv.Itoa(i)
		msgs[i] = Message{
			ID: id, Key: "k" + id, MessageID: "<" + id + "@x>",
			Subject: "Subject " + strconv.Itoa(i), Date: base.Add(time.Duration(i) * time.Minute),
		}
	}
	return msgs
}
