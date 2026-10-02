// Command ivy-assets precompresses a built frontend directory in place. It is
// a build-time tool (make web-assets, the container image build) and is never
// shipped in the production binary.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/AutumnsGrove/Ivy/internal/asset"
)

func main() {
	dir := flag.String("dir", "internal/webui/build", "directory to precompress in place")
	flag.Parse()

	stats, err := asset.Precompress(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ivy-assets:", err)
		os.Exit(1)
	}
	fmt.Printf("precompressed %d of %d assets in %s\n", stats.Compressed, stats.Scanned, *dir)
}
