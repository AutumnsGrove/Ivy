// Spike S6: is pure-Go PDF text extraction good enough? usage: s6 <lib> <file.pdf>
// lib is one of: ledongthuc, dslipak, pdfium (go-pdfium WebAssembly via wazero, still no cgo).
// Prints one JSON line; run each combination in its own process so memory is attributable.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	dslipak "github.com/dslipak/pdf"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
	ledongthuc "github.com/ledongthuc/pdf"
)

type result struct {
	Lib, File string
	Pages     int
	Words     int
	Ms        int64
	HeapMiB   uint64
	Err       string
	Panic     string
	Sample    string `json:",omitempty"`
}

func main() {
	if len(os.Args) != 3 {
		fmt.Println("usage: s6 <lib> <file>")
		os.Exit(2)
	}
	lib, file := os.Args[1], os.Args[2]
	res := result{Lib: lib, File: file}
	start := time.Now()
	var text string
	func() {
		defer func() {
			if r := recover(); r != nil {
				res.Panic = fmt.Sprint(r)
			}
		}()
		var err error
		switch lib {
		case "ledongthuc":
			text, res.Pages, err = viaLedongthuc(file)
		case "dslipak":
			text, res.Pages, err = viaDslipak(file)
		case "pdfium":
			text, res.Pages, err = viaPdfium(file)
		default:
			err = fmt.Errorf("unknown lib %q", lib)
		}
		if err != nil {
			res.Err = err.Error()
		}
	}()
	res.Ms = time.Since(start).Milliseconds()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	res.HeapMiB = ms.Sys / (1 << 20)
	res.Words = len(strings.Fields(text))
	if len(os.Getenv("S6_SAMPLE")) > 0 { // only for the synthetic fixtures, never the real file
		res.Sample = strings.Join(strings.Fields(text), " ")
	}
	if dump := os.Getenv("S6_DUMP"); dump != "" {
		_ = os.WriteFile(dump, []byte(text), 0o600)
	}
	b, _ := json.Marshal(res)
	fmt.Println(string(b))
}

func viaLedongthuc(path string) (string, int, error) {
	f, r, err := ledongthuc.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	var sb strings.Builder
	for i := 1; i <= r.NumPage(); i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		t, err := p.GetPlainText(nil)
		if err != nil {
			return sb.String(), r.NumPage(), fmt.Errorf("page %d: %w", i, err)
		}
		sb.WriteString(t)
		sb.WriteByte('\n')
	}
	return sb.String(), r.NumPage(), nil
}

func viaDslipak(path string) (string, int, error) {
	r, err := dslipak.Open(path)
	if err != nil {
		return "", 0, err
	}
	rd, err := r.GetPlainText()
	if err != nil {
		return "", r.NumPage(), err
	}
	b, err := io.ReadAll(rd)
	return string(b), r.NumPage(), err
}

func viaPdfium(path string) (string, int, error) {
	pool, err := webassembly.Init(webassembly.Config{MinIdle: 1, MaxIdle: 1, MaxTotal: 1})
	if err != nil {
		return "", 0, err
	}
	defer pool.Close()
	inst, err := pool.GetInstance(time.Minute)
	if err != nil {
		return "", 0, err
	}
	defer inst.Close()
	doc, err := inst.OpenDocument(&requests.OpenDocument{FilePath: &path})
	if err != nil {
		return "", 0, err
	}
	defer inst.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: doc.Document})
	pc, err := inst.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: doc.Document})
	if err != nil {
		return "", 0, err
	}
	var sb strings.Builder
	for i := 0; i < pc.PageCount; i++ {
		t, err := inst.GetPageText(&requests.GetPageText{Page: requests.Page{ByIndex: &requests.PageByIndex{Document: doc.Document, Index: i}}})
		if err != nil {
			return sb.String(), pc.PageCount, fmt.Errorf("page %d: %w", i, err)
		}
		sb.WriteString(t.Text)
		sb.WriteByte('\n')
	}
	return sb.String(), pc.PageCount, nil
}
