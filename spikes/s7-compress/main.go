// Spike S7: what does compression cost, in size and CPU, on the target board?
// usage: s7 <web-build-dir> <docs-dir> [only]   (only = e.g. "br11" to run one codec in isolation)
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

type codec struct {
	name string
	run  func(in []byte) []byte
}

func gz(level int) codec {
	return codec{fmt.Sprintf("gzip%d", level), func(in []byte) []byte {
		var b bytes.Buffer
		w, _ := gzip.NewWriterLevel(&b, level)
		_, _ = w.Write(in)
		_ = w.Close()
		return b.Bytes()
	}}
}

func br(level int) codec {
	return codec{fmt.Sprintf("br%d", level), func(in []byte) []byte {
		var b bytes.Buffer
		w := brotli.NewWriterLevel(&b, level)
		_, _ = w.Write(in)
		_ = w.Close()
		return b.Bytes()
	}}
}

func zs(name string, level zstd.EncoderLevel) codec {
	enc, _ := zstd.NewWriter(nil, zstd.WithEncoderLevel(level), zstd.WithEncoderConcurrency(1))
	return codec{"zstd-" + name, func(in []byte) []byte { return enc.EncodeAll(in, nil) }}
}

func codecs() []codec {
	return []codec{
		gz(1), gz(6), gz(9), br(1), br(4), br(5), br(6), br(9), br(11),
		zs("fastest", zstd.SpeedFastest), zs("default", zstd.SpeedDefault), zs("better", zstd.SpeedBetterCompression), zs("best", zstd.SpeedBestCompression),
	}
}

func main() {
	buildDir, docsDir := os.Args[1], os.Args[2]
	only := ""
	if len(os.Args) > 3 {
		only = os.Args[3]
	}
	fmt.Printf("GOOS=%s GOARCH=%s CPUs=%d (each codec single-threaded)\n", runtime.GOOS, runtime.GOARCH, runtime.NumCPU())

	// Static corpus: every compressible file in the real built frontend.
	var static [][]byte
	var staticBytes int
	_ = filepath.Walk(buildDir, func(p string, i os.FileInfo, err error) error {
		if err == nil && !i.IsDir() {
			switch filepath.Ext(p) {
			case ".js", ".css", ".html", ".svg", ".json", ".txt", ".webmanifest":
				if b, err := os.ReadFile(p); err == nil {
					static = append(static, b)
					staticBytes += len(b)
				}
			}
		}
		return nil
	})
	fmt.Printf("static corpus: %d files, %d KiB raw\n", len(static), staticBytes/1024)

	prose := loadProse(docsDir)
	dyn := map[string][]byte{
		"list25 (typical page)": listJSON(25, prose),
		"list200 (large page)":  listJSON(200, prose),
		"thread (60 KB bodies)": threadJSON(prose),
	}
	for k, v := range dyn {
		fmt.Printf("dynamic %-24s %6d bytes raw\n", k, len(v))
	}

	fmt.Printf("\n%-14s | %-26s | %-34s | %-34s | %-34s\n", "codec", "static (whole build)", "list25", "list200", "thread")
	for _, c := range codecs() {
		if only != "" && c.name != only {
			continue
		}
		var sOut int
		sDur := timeIt(func() {
			sOut = 0
			for _, f := range static {
				sOut += len(c.run(f))
			}
		}, 1)
		fmt.Printf("%-14s | %6d KiB %5.1f%% %6dms | ", c.name, sOut/1024, 100*float64(sOut)/float64(staticBytes), sDur.Milliseconds())
		for _, k := range []string{"list25 (typical page)", "list200 (large page)", "thread (60 KB bodies)"} {
			in := dyn[k]
			var out int
			d := timeIt(func() { out = len(c.run(in)) }, 20)
			fmt.Printf("%6d B %5.1f%% %8.2fms | ", out, 100*float64(out)/float64(len(in)), float64(d.Microseconds())/1000)
		}
		fmt.Println()
	}
}

// timeIt returns the mean duration of f over at least n runs and at least 300 ms.
func timeIt(f func(), n int) time.Duration {
	if n > 1 {
		f() // warm-up; skipped for the single whole-build pass, which can take a long time
	}
	start := time.Now()
	runs := 0
	for runs < n || time.Since(start) < 300*time.Millisecond {
		f()
		runs++
		if n == 1 && runs >= 1 {
			break
		}
	}
	return time.Since(start) / time.Duration(runs)
}

func loadProse(dir string) []string {
	var words []string
	files, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil {
			words = append(words, strings.Fields(string(b))...)
		}
	}
	return words
}

func sentence(r *rand.Rand, w []string, n int) string {
	s := r.Intn(len(w) - n)
	return strings.Join(w[s:s+n], " ")
}

func listJSON(n int, prose []string) []byte {
	r := rand.New(rand.NewSource(1))
	type item struct {
		ID      string   `json:"id"`
		Thread  string   `json:"thread_id"`
		From    string   `json:"from"`
		Subject string   `json:"subject"`
		Snippet string   `json:"snippet"`
		Date    string   `json:"date"`
		Flags   []string `json:"flags"`
		Tags    []string `json:"tags"`
		Size    int      `json:"size"`
		Account string   `json:"account"`
	}
	var items []item
	for i := 0; i < n; i++ {
		items = append(items, item{
			ID: fmt.Sprintf("msg_%016x", r.Uint64()), Thread: fmt.Sprintf("thr_%016x", r.Uint64()),
			From:    sentence(r, prose, 2) + " <" + strings.ToLower(strings.Trim(prose[r.Intn(len(prose))], "`*#|.,")) + "@example.com>",
			Subject: sentence(r, prose, 6), Snippet: sentence(r, prose, 22),
			Date: time.Unix(1790000000-int64(i)*3600, 0).UTC().Format(time.RFC3339), Flags: []string{"seen"}, Tags: []string{"receipts"},
			Size: 2000 + r.Intn(60000), Account: "acc_main",
		})
	}
	b, _ := json.Marshal(map[string]any{"items": items, "next_cursor": "c_" + fmt.Sprint(r.Uint64())})
	return b
}

func threadJSON(prose []string) []byte {
	r := rand.New(rand.NewSource(2))
	var msgs []map[string]string
	for i := 0; i < 4; i++ {
		text := sentence(r, prose, 2000)
		if len(text) > 15000 {
			text = text[:15000]
		}
		msgs = append(msgs, map[string]string{"id": fmt.Sprintf("msg_%d", i), "from": "someone@example.com", "text": text})
	}
	b, _ := json.Marshal(map[string]any{"messages": msgs})
	return b
}
