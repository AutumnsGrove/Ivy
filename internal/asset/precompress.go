// Package asset precompresses the embedded frontend at build time and serves
// it with the best coding a client accepts.
package asset

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// Precompressed file suffixes, one per coding. The static handler tries these
// in preference order and falls back to the original.
const (
	brSuffix  = ".br"
	zstSuffix = ".zst"
	gzSuffix  = ".gz"
)

// maxAssetSize bounds a single build asset so a stray huge file cannot be read
// into memory (STANDARDS.md: no unbounded reads).
const maxAssetSize = 64 << 20

// Stats summarises a Precompress run.
type Stats struct {
	Scanned    int // regular files walked
	Compressed int // files that gained at least one smaller variant
}

// Precompress writes brotli (11), zstd (best) and gzip (9) siblings next to
// every compressible file under dir. Already-compressed files are left alone,
// and a variant is only written when it is actually smaller.
func Precompress(dir string) (Stats, error) {
	var stats Stats
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() || !precompressible(path) {
			return nil
		}
		stats.Scanned++
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxAssetSize {
			return fmt.Errorf("precompress %s: %d bytes exceeds the %d byte limit", path, info.Size(), maxAssetSize)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		wrote := false
		for _, variant := range variants {
			encoded, err := variant.encode(data)
			if err != nil {
				return fmt.Errorf("precompress %s as %s: %w", path, variant.suffix, err)
			}
			if len(encoded) >= len(data) {
				// Drop a sibling an earlier run left, or it would be served for
				// content it no longer matches.
				if err := os.Remove(path + variant.suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return err
				}
				continue
			}
			if err := os.WriteFile(path+variant.suffix, encoded, 0o644); err != nil {
				return err
			}
			wrote = true
		}
		if wrote {
			stats.Compressed++
		}
		return nil
	})
	if err != nil {
		return stats, err
	}
	return stats, nil
}

type variant struct {
	suffix string
	encode func([]byte) ([]byte, error)
}

// Static variants use maximum levels: they run once at build time, never on the
// board (S7).
var variants = []variant{
	{brSuffix, encodeBrotli},
	{zstSuffix, encodeZstd},
	{gzSuffix, encodeGzip},
}

func encodeBrotli(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := brotli.NewWriterLevel(&buf, brotli.BestCompression)
	if _, err := zw.Write(data); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodeZstd(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf, zstd.WithEncoderLevel(zstd.SpeedBestCompression))
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(data); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodeGzip(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(data); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// compressibleExt lists the text (and wasm) extensions worth precompressing.
// Fonts, images and archives are deliberately absent.
var compressibleExt = map[string]bool{
	".cjs":         true,
	".css":         true,
	".htm":         true,
	".html":        true,
	".js":          true,
	".json":        true,
	".map":         true,
	".mjs":         true,
	".svg":         true,
	".txt":         true,
	".wasm":        true,
	".webmanifest": true,
	".xml":         true,
}

func precompressible(path string) bool {
	name := filepath.Base(path)
	if strings.HasSuffix(name, brSuffix) || strings.HasSuffix(name, zstSuffix) || strings.HasSuffix(name, gzSuffix) {
		return false
	}
	return compressibleExt[strings.ToLower(filepath.Ext(name))]
}
