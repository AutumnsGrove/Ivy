package devstack

import (
	"fmt"
	"io"

	"rsc.io/qr"
)

// WriteQR renders text as a terminal QR code using half-block characters, so a
// phone can scan the tailnet URL from `ivy-dev up --expose` (DEV.md section 1).
func WriteQR(w io.Writer, text string) error {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return fmt.Errorf("devstack: encode QR: %w", err)
	}
	const quiet = 2
	size := code.Size
	for y := -quiet; y < size+quiet; y += 2 {
		var row []byte
		for x := -quiet; x < size+quiet; x++ {
			top := blackAt(code, x, y)
			bottom := blackAt(code, x, y+1)
			switch {
			case top && bottom:
				row = append(row, "█"...)
			case top:
				row = append(row, "▀"...)
			case bottom:
				row = append(row, "▄"...)
			default:
				row = append(row, ' ')
			}
		}
		if _, err := fmt.Fprintln(w, string(row)); err != nil {
			return err
		}
	}
	return nil
}

// blackAt reports a module's colour, treating the quiet zone as white.
func blackAt(code *qr.Code, x, y int) bool {
	if x < 0 || y < 0 || x >= code.Size || y >= code.Size {
		return false
	}
	return code.Black(x, y)
}
