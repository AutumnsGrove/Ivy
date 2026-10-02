package compress

import "testing"

func TestNegotiate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		header string
		want   Encoding
	}{
		{"empty means identity", "", Identity},
		{"gzip only", "gzip", Gzip},
		{"brotli only", "br", Brotli},
		{"zstd only", "zstd", Zstd},
		{"safari advertises all, zstd wins", "gzip, deflate, br, zstd", Zstd},
		{"brotli beats gzip on equal q", "gzip, br", Brotli},
		{"higher q wins over preference", "zstd;q=0.5, gzip;q=0.9", Gzip},
		{"explicit zero disables", "gzip;q=0, br, zstd", Zstd},
		{"all supported refused", "gzip;q=0, br;q=0, zstd;q=0", Identity},
		{"wildcard picks our best", "*", Zstd},
		{"wildcard refused", "*;q=0", Identity},
		{"identity only", "identity", Identity},
		{"identity refused, nothing else offered", "identity;q=0", Identity},
		{"unsupported coding ignored", "deflate", Identity},
		{"case and whitespace tolerated", " GZIP ; q=0.4 , BR ", Brotli},
		{"malformed q is not acceptable", "gzip;q=oops, br", Brotli},
		{"uppercase wildcard", "*;Q=0.5", Zstd},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Negotiate(tt.header); got != tt.want {
				t.Errorf("Negotiate(%q) = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}

func TestCompressible(t *testing.T) {
	t.Parallel()
	tests := []struct {
		contentType string
		want        bool
	}{
		{"application/json", true},
		{"application/json; charset=utf-8", true},
		{"application/ld+json", true},
		{"text/html", true},
		{"text/plain; charset=utf-8", true},
		{"text/event-stream", true},
		{"application/javascript", true},
		{"text/javascript", true},
		{"application/xml", true},
		{"application/xhtml+xml", true},
		{"image/svg+xml", true},
		{"application/wasm", true},
		{"", false},
		{"image/jpeg", false},
		{"image/png", false},
		{"font/woff2", false},
		{"application/octet-stream", false},
		{"application/zip", false},
	}
	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			t.Parallel()
			if got := compressibleContentType(tt.contentType); got != tt.want {
				t.Errorf("compressibleContentType(%q) = %v, want %v", tt.contentType, got, tt.want)
			}
		})
	}
}
