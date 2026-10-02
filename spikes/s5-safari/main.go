// Spike S5: how does Safari behave? A throwaway test server for the operator's own devices over
// Tailscale. Stores no uploaded files (metadata only) and writes reports to the git-ignored .dev/.
package main

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

//go:embed index.html
var indexHTML []byte

var (
	mu      sync.Mutex
	hits    = map[string]int{} // remote-content and navigation probes, by key
	reports *os.File
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: s5 <tailscale-ip:port> [local-addr-for-tailscale-serve]")
	}
	_ = os.MkdirAll("../../.dev", 0o700)
	var err error
	if reports, err = os.OpenFile("../../.dev/s5-reports.jsonl", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexHTML)
	})
	mux.HandleFunc("/api/echo", echo)
	mux.HandleFunc("/api/upload", upload)
	mux.HandleFunc("/api/report", report)
	mux.HandleFunc("/api/hits", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		_ = json.NewEncoder(w).Encode(hits)
	})
	mux.HandleFunc("/api/reset", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = map[string]int{}
		mu.Unlock()
	})
	mux.HandleFunc("/enc/", encoded)
	mux.HandleFunc("/mail/hostile", hostile)
	mux.HandleFunc("/probe/", probe) // remote image / navigation targets

	h := logRequests(mux)
	for _, addr := range os.Args[1:] {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			log.Fatal(err)
		}
		log.Println("listening on", addr)
		go func() { log.Fatal((&http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}).Serve(ln)) }()
	}
	select {}
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func echo(w http.ResponseWriter, r *http.Request) {
	out := map[string]string{
		"accept-encoding": r.Header.Get("Accept-Encoding"),
		"accept":          r.Header.Get("Accept"),
		"user-agent":      r.Header.Get("User-Agent"),
		"x-forwarded":     r.Header.Get("X-Forwarded-Proto"),
		"host":            r.Host,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func report(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	mu.Lock()
	defer mu.Unlock()
	line := fmt.Sprintf(`{"t":%q,"ua":%q,"ae":%q,"data":%s}`+"\n", time.Now().Format(time.RFC3339), r.UserAgent(), r.Header.Get("Accept-Encoding"), json.RawMessage(b))
	_, _ = reports.WriteString(line)
}

// upload reports what the browser actually sent; nothing is stored.
func upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	for {
		p, err := mr.NextPart()
		if err != nil {
			http.Error(w, "no file part", 400)
			return
		}
		if p.FileName() == "" {
			continue
		}
		head := make([]byte, 1<<20)
		n, _ := io.ReadFull(p, head)
		head = head[:n]
		rest, _ := io.Copy(io.Discard, p)
		size := int64(n) + rest
		out := map[string]any{
			"filename":        p.FileName(),
			"declared_type":   p.Header.Get("Content-Type"),
			"size":            size,
			"first_bytes":     hex.EncodeToString(head[:min(16, len(head))]),
			"sniffed_type":    http.DetectContentType(head),
			"heic_like":       isHEIC(head),
			"decodes_in_go":   false,
			"width_x_height":  "",
			"image_format_go": "",
		}
		if cfg, format, err := image.DecodeConfig(bytes.NewReader(head)); err == nil {
			out["decodes_in_go"] = true
			out["width_x_height"] = fmt.Sprintf("%dx%d", cfg.Width, cfg.Height)
			out["image_format_go"] = format
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
		return
	}
}

func isHEIC(b []byte) bool {
	if len(b) < 12 || string(b[4:8]) != "ftyp" {
		return false
	}
	switch string(b[8:12]) {
	case "heic", "heix", "hevc", "heim", "heis", "mif1", "msf1":
		return true
	}
	return false
}

// encoded serves the same compressible JSON with a forced Content-Encoding, regardless of what the
// browser asked for, to learn which encodings Safari can really decode.
func encoded(w http.ResponseWriter, r *http.Request) {
	enc := strings.TrimPrefix(r.URL.Path, "/enc/")
	var items []map[string]any
	for i := 0; i < 1500; i++ {
		items = append(items, map[string]any{"id": i, "subject": fmt.Sprintf("Message number %d about gardens", i), "tags": []string{"receipts", "garden"}})
	}
	raw, _ := json.Marshal(map[string]any{"count": len(items), "items": items})
	var buf bytes.Buffer
	switch enc {
	case "identity":
		buf.Write(raw)
	case "gzip":
		zw := gzip.NewWriter(&buf)
		_, _ = zw.Write(raw)
		_ = zw.Close()
	case "br":
		bw := brotli.NewWriterLevel(&buf, 5)
		_, _ = bw.Write(raw)
		_ = bw.Close()
	case "zstd":
		zw, _ := zstd.NewWriter(&buf)
		_, _ = zw.Write(raw)
		_ = zw.Close()
	default:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Raw-Length", fmt.Sprint(len(raw)))
	if enc != "identity" {
		w.Header().Set("Content-Encoding", enc)
	}
	_, _ = w.Write(buf.Bytes())
}

// hostile is an email-shaped page full of things a mail renderer must neutralise. `case` labels
// which iframe loaded it; `csp=1` adds a strict Content-Security-Policy header.
func hostile(w http.ResponseWriter, r *http.Request) {
	c := r.URL.Query().Get("case")
	if r.URL.Query().Get("csp") == "1" {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src data:; style-src 'unsafe-inline'")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(hostileHTML(c, "")))
}

func hostileHTML(c, origin string) string {
	return `<html><body style="font-family:sans-serif">
<p>Hostile mail body (case ` + c + `)</p>
<img src="` + origin + `/probe/img?case=` + c + `" width="8" height="8">
<script>try{parent.postMessage("script-ran:` + c + `","*")}catch(e){}</script>
<meta http-equiv="refresh" content="1;url=` + origin + `/probe/nav?case=` + c + `">
<form action="` + origin + `/probe/form?case=` + c + `" method="post"><input name="x"></form>
<a href="` + origin + `/probe/link?case=` + c + `">a link</a>
</body></html>`
}

func probe(w http.ResponseWriter, r *http.Request) {
	kind := strings.TrimPrefix(r.URL.Path, "/probe/")
	key := kind + ":" + r.URL.Query().Get("case")
	mu.Lock()
	hits[key]++
	mu.Unlock()
	if kind == "img" {
		w.Header().Set("Content-Type", "image/gif")
		_, _ = w.Write([]byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;"))
		return
	}
	_, _ = w.Write([]byte("probe " + key))
}
