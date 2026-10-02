package asset

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

// benchAsset is about the size of one fingerprinted JS chunk, so the per-request
// copy and SHA-256 of N4 are actually exercised.
var benchAsset = bytes.Repeat([]byte("export const answer = () => 42; // ivy garden\n"), 6000)

const benchAssetPath = "_app/immutable/chunk.abcdef123456.js"

func benchFS(b *testing.B) fstest.MapFS {
	b.Helper()
	return fstest.MapFS{
		benchAssetPath:             &fstest.MapFile{Data: benchAsset},
		benchAssetPath + brSuffix:  &fstest.MapFile{Data: mustEncode(b, brSuffix, benchAsset)},
		benchAssetPath + zstSuffix: &fstest.MapFile{Data: mustEncode(b, zstSuffix, benchAsset)},
		benchAssetPath + gzSuffix:  &fstest.MapFile{Data: mustEncode(b, gzSuffix, benchAsset)},
	}
}

func benchFileServer(b *testing.B, accept string) {
	h := FileServer(benchFS(b))
	req := httptest.NewRequest(http.MethodGet, "/"+benchAssetPath, nil)
	if accept == "" {
		req.Header["Accept-Encoding"] = nil
	} else {
		req.Header.Set("Accept-Encoding", accept)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(benchAsset)))
	b.ResetTimer()
	for range b.N {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
	}
}

func BenchmarkFileServerIdentity(b *testing.B) { benchFileServer(b, "") }
func BenchmarkFileServerZstd(b *testing.B)     { benchFileServer(b, "zstd") }
