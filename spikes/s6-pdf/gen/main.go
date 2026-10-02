// Generates synthetic receipt and invoice PDFs with known text, plus an image-only "scan", into
// ../../../.dev/s6/. Also extracts the first PDF attachment from a local .eml (not committed).
package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"strings"

	"github.com/go-pdf/fpdf"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

const out = "../../../.dev/s6/"

func main() {
	if err := os.MkdirAll(out, 0o700); err != nil {
		log.Fatal(err)
	}
	receipt()
	invoice()
	scan()
	extractAttachment("../../../.dev/s1-inbox/5.eml")
}

func receipt() {
	p := fpdf.New("P", "mm", "A4", "")
	p.AddPage()
	p.SetFont("Helvetica", "B", 16)
	p.Cell(0, 10, "Wildflower Garden Supply")
	p.Ln(10)
	p.SetFont("Helvetica", "", 11)
	for _, l := range []string{"Receipt for order 4821", "Date: 2026-09-28", "Ceramic planter x1   24.00", "Shipping   4.00", "Total paid: USD 28.00", "Card ending 4242"} {
		p.Cell(0, 7, l)
		p.Ln(7)
	}
	if err := p.OutputFileAndClose(out + "receipt.pdf"); err != nil {
		log.Fatal(err)
	}
}

// invoice has a two-column table and a right-aligned totals block, the layout that trips
// extractors which read in content-stream order.
func invoice() {
	p := fpdf.New("P", "mm", "A4", "")
	p.SetCompression(true)
	p.AddPage()
	p.SetFont("Helvetica", "B", 14)
	p.Cell(120, 8, "Northfield Hosting Ltd")
	p.CellFormat(0, 8, "INVOICE 2026-0912", "", 1, "R", false, 0, "")
	p.SetFont("Helvetica", "", 10)
	p.Cell(120, 6, "Billed to: Example Customer")
	p.CellFormat(0, 6, "Due date: 2026-10-15", "", 1, "R", false, 0, "")
	p.Ln(6)
	for _, r := range [][3]string{{"Description", "Qty", "Amount"}, {"Managed hosting, September", "1", "10.00"}, {"Extra storage 50 GB", "1", "2.00"}} {
		p.CellFormat(110, 7, r[0], "1", 0, "L", false, 0, "")
		p.CellFormat(30, 7, r[1], "1", 0, "C", false, 0, "")
		p.CellFormat(0, 7, r[2], "1", 1, "R", false, 0, "")
	}
	p.Ln(4)
	p.SetFont("Helvetica", "B", 11)
	p.CellFormat(0, 7, "Amount due: USD 12.00", "", 1, "R", false, 0, "")
	if err := p.OutputFileAndClose(out + "invoice.pdf"); err != nil {
		log.Fatal(err)
	}
}

// scan draws text into a PNG and embeds only the image: no text layer, like a scanned receipt.
func scan() {
	img := image.NewGray(image.Rect(0, 0, 600, 200))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	d := &font.Drawer{Dst: img, Src: image.NewUniform(color.Gray{0}), Face: basicfont.Face7x13, Dot: fixed.P(20, 40)}
	d.DrawString("SCANNED RECEIPT  Total paid USD 28.00")
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		log.Fatal(err)
	}
	p := fpdf.New("P", "mm", "A4", "")
	p.AddPage()
	p.RegisterImageOptionsReader("scan", fpdf.ImageOptions{ImageType: "PNG"}, &buf)
	p.ImageOptions("scan", 10, 10, 190, 0, false, fpdf.ImageOptions{ImageType: "PNG"}, 0, "")
	if err := p.OutputFileAndClose(out + "scan.pdf"); err != nil {
		log.Fatal(err)
	}
}

func extractAttachment(path string) {
	f, err := os.Open(path)
	if err != nil {
		fmt.Println("no local test mail, skipping real PDF:", err)
		return
	}
	defer f.Close()
	m, err := mail.ReadMessage(f)
	if err != nil {
		log.Fatal(err)
	}
	mt, params, _ := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if !strings.HasPrefix(mt, "multipart/") {
		return
	}
	mr := multipart.NewReader(m.Body, params["boundary"])
	for {
		part, err := mr.NextPart()
		if err != nil {
			return
		}
		if strings.HasPrefix(part.Header.Get("Content-Type"), "application/pdf") {
			b, _ := io.ReadAll(part)
			// multipart.Part decodes quoted-printable but not base64.
			if strings.EqualFold(part.Header.Get("Content-Transfer-Encoding"), "base64") {
				clean := strings.NewReplacer("\r", "", "\n", "", " ", "").Replace(string(b))
				if b, err = base64.StdEncoding.DecodeString(clean); err != nil {
					log.Fatal(err)
				}
			}
			if err := os.WriteFile(out+"real.pdf", b, 0o600); err != nil {
				log.Fatal(err)
			}
			fmt.Printf("extracted real PDF: %d bytes\n", len(b))
			return
		}
	}
}
