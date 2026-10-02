// Spike S8: embedding throughput and brute-force search cost on the board.
//
//	s8 embed <docs-dir> <vectors-out>   embed repo prose chunks via local Ollama, record rates
//	s8 search <vectors-in>              recall of int8 / truncated search vs exact, and timing at 100k
package main

import (
	"bytes"
	"container/heap"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const dim = 768

func main() {
	switch os.Args[1] {
	case "embed":
		embed(os.Args[2], os.Args[3])
	case "search":
		search(os.Args[2])
	}
}

func chunks(dir string, size int) []string {
	files, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	sort.Strings(files)
	var out []string
	for _, f := range files {
		b, _ := os.ReadFile(f)
		words := strings.Fields(string(b))
		var cur []string
		n := 0
		for _, w := range words {
			cur = append(cur, w)
			n += len(w) + 1
			if n >= size {
				out = append(out, strings.Join(cur, " "))
				cur, n = nil, 0
			}
		}
	}
	return out
}

func embedBatch(texts []string, keep string) ([][]float32, time.Duration, error) {
	body, _ := json.Marshal(map[string]any{"model": "nomic-embed-text", "input": texts, "keep_alive": keep})
	t0 := time.Now()
	resp, err := http.Post("http://localhost:11434/api/embed", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	d := time.Since(t0)
	var r struct {
		Embeddings [][]float32 `json:"embeddings"`
		Error      string      `json:"error"`
	}
	if err := json.Unmarshal(b, &r); err != nil || r.Error != "" {
		return nil, d, fmt.Errorf("bad response: %v %s", err, r.Error)
	}
	return r.Embeddings, d, nil
}

func embed(dir, outPath string) {
	size := 1500 // about 350 tokens: a typical email body
	if v := os.Getenv("S8_CHUNK"); v != "" {
		fmt.Sscan(v, &size)
	}
	cs := chunks(dir, size)
	if len(cs) > 600 {
		cs = cs[:600]
	}
	for i := range cs {
		cs[i] = "search_document: " + cs[i]
	}
	fmt.Printf("%d chunks, mean %d chars\n", len(cs), meanLen(cs))
	_, d, err := embedBatch(cs[:1], "5m") // load the model: cold-start cost
	if err != nil {
		fmt.Println("first call failed:", err)
		os.Exit(1)
	}
	fmt.Printf("cold first call (model load + 1 embed): %v\n", d.Round(time.Millisecond))

	var all [][]float32
	for _, bs := range []int{1, 8, 32} {
		n := 48
		start := time.Now()
		for i := 0; i < n; i += bs {
			vs, _, err := embedBatch(cs[i:min(i+bs, len(cs))], "5m")
			if err != nil {
				fmt.Println("batch failed:", err)
				os.Exit(1)
			}
			_ = vs
		}
		el := time.Since(start)
		fmt.Printf("batch=%-2d  %d texts in %v  => %.2f texts/s, %.0f ms/text\n", bs, n, el.Round(time.Millisecond), float64(n)/el.Seconds(), float64(el.Milliseconds())/float64(n))
	}
	// Embed everything (batch 8) for the quality test, keeping the vectors.
	start := time.Now()
	for i := 0; i < len(cs); i += 8 {
		keep := "5m"
		if i+8 >= len(cs) {
			keep = "0" // unload the model afterwards to give the memory back
		}
		vs, _, err := embedBatch(cs[i:min(i+8, len(cs))], keep)
		if err != nil {
			fmt.Println("batch failed:", err)
			os.Exit(1)
		}
		all = append(all, vs...)
	}
	el := time.Since(start)
	fmt.Printf("all %d chunks: %v => %.2f texts/s; 1000 messages would take about %.1f min\n", len(all), el.Round(time.Second), float64(len(all))/el.Seconds(), 1000/(float64(len(all))/el.Seconds())/60)
	f, _ := os.Create(outPath)
	defer f.Close()
	for _, v := range all {
		_ = binary.Write(f, binary.LittleEndian, v)
	}
}

func meanLen(s []string) int {
	t := 0
	for _, x := range s {
		t += len(x)
	}
	return t / len(s)
}

// ---- search ----

func normalize(v []float32) {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	n := float32(1 / math.Sqrt(s))
	for i := range v {
		v[i] *= n
	}
}

func dotF(a, b []float32) float32 {
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

func quant(v []float32) []int8 {
	q := make([]int8, len(v))
	for i, x := range v {
		q[i] = int8(math.Round(float64(x) * 127))
	}
	return q
}

func dotQ(a, b []int8) int32 {
	var s int32
	for i := range a {
		s += int32(a[i]) * int32(b[i])
	}
	return s
}

type hit struct {
	id    int
	score float64
}
type minHeap []hit

func (h minHeap) Len() int           { return len(h) }
func (h minHeap) Less(i, j int) bool { return h[i].score < h[j].score }
func (h minHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *minHeap) Push(x any)        { *h = append(*h, x.(hit)) }
func (h *minHeap) Pop() any          { o := *h; x := o[len(o)-1]; *h = o[:len(o)-1]; return x }

func topK(n, k int, score func(i int) float64) []int {
	h := &minHeap{}
	for i := 0; i < n; i++ {
		s := score(i)
		if h.Len() < k {
			heap.Push(h, hit{i, s})
		} else if s > (*h)[0].score {
			(*h)[0] = hit{i, s}
			heap.Fix(h, 0)
		}
	}
	out := make([]int, 0, k)
	sorted := append([]hit(nil), *h...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].score > sorted[j].score })
	for _, x := range sorted {
		out = append(out, x.id)
	}
	return out
}

func overlap(a, b []int) float64 {
	m := map[int]bool{}
	for _, x := range a {
		m[x] = true
	}
	c := 0
	for _, x := range b {
		if m[x] {
			c++
		}
	}
	return float64(c) / float64(len(a))
}

func search(path string) {
	raw, _ := os.ReadFile(path)
	n := len(raw) / (dim * 4)
	vecs := make([][]float32, n)
	for i := range vecs {
		v := make([]float32, dim)
		for j := range v {
			v[j] = math.Float32frombits(binary.LittleEndian.Uint32(raw[(i*dim+j)*4:]))
		}
		normalize(v)
		vecs[i] = v
	}
	fmt.Printf("%d real vectors of %d dims\n", n, dim)

	// Quality: for 100 queries (vectors themselves, minus the self-match), recall@10 of cheaper
	// representations against exact float32 cosine.
	// Each variant truncates to d dimensions (re-normalising), and optionally quantises to int8
	// with ONE scale for the whole set, taken from the data's largest component. A fixed 127
	// scale would crush unit-vector components (about 0.04) to a few integer levels.
	type variant struct {
		name string
		d    int
		int8 bool
	}
	variants := []variant{{"int8 768d", 768, true}, {"int8 512d", 512, true}, {"int8 256d", 256, true}, {"float32 512d", 512, false}, {"float32 256d", 256, false}}
	nq := min(100, n)
	exact := make([][]int, nq)
	for qi := 0; qi < nq; qi++ {
		exact[qi] = topK(n, 11, func(i int) float64 { return float64(dotF(vecs[qi], vecs[i])) })[1:] // drop self
	}
	for _, v := range variants {
		fl := make([][]float32, n)
		var maxAbs float64
		for i := range vecs {
			c := append([]float32(nil), vecs[i][:v.d]...)
			normalize(c)
			fl[i] = c
			for _, x := range c {
				maxAbs = math.Max(maxAbs, math.Abs(float64(x)))
			}
		}
		var score func(a, b int) float64
		if v.int8 {
			q := make([][]int8, n)
			for i := range fl {
				q[i] = make([]int8, v.d)
				for j, x := range fl[i] {
					q[i][j] = int8(math.Round(float64(x) / maxAbs * 127))
				}
			}
			score = func(a, b int) float64 { return float64(dotQ(q[a], q[b])) }
		} else {
			score = func(a, b int) float64 { return float64(dotF(fl[a], fl[b])) }
		}
		var rec float64
		for qi := 0; qi < nq; qi++ {
			got := topK(n, 11, func(i int) float64 { return score(qi, i) })[1:]
			rec += overlap(exact[qi], got)
		}
		fmt.Printf("recall@10 vs exact float32 768d: %-12s %.3f\n", v.name, rec/float64(nq))
	}

	// Timing and memory at 100k synthetic vectors (random unit vectors; speed only).
	const big = 100000
	r := rand.New(rand.NewSource(1))
	for _, d := range []int{768, 256} {
		store := make([]int8, big*d)
		for i := range store {
			store[i] = int8(r.Intn(255) - 127)
		}
		q := store[:d]
		var ms runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&ms)
		var tot time.Duration
		const runs = 5
		for k := 0; k < runs; k++ {
			t0 := time.Now()
			_ = topK(big, 10, func(i int) float64 { return float64(dotQ(q, store[i*d:(i+1)*d])) })
			tot += time.Since(t0)
		}
		fmt.Printf("int8 %3dd x %d vectors: %.1f MiB, top-10 brute force %.1f ms per query (mean of %d)\n", d, big, float64(len(store))/(1<<20), float64(tot.Milliseconds())/runs, runs)
	}
	{
		d := 768
		const n32 = 20000 // 20k, to project 100k without exhausting the board's memory
		store := make([]float32, n32*d)
		for i := range store {
			store[i] = float32(r.NormFloat64())
		}
		q := store[:d]
		t0 := time.Now()
		const runs = 3
		for k := 0; k < runs; k++ {
			_ = topK(n32, 10, func(i int) float64 { return float64(dotF(q, store[i*d:(i+1)*d])) })
		}
		per := float64(time.Since(t0).Milliseconds()) / runs
		fmt.Printf("float32 768d x %d vectors: %.1f MiB, %.1f ms per query => about %.0f ms and %.0f MiB at 100k\n", n32, float64(len(store)*4)/(1<<20), per, per*5, float64(len(store)*4)/(1<<20)*5)
	}
}
