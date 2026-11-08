package publish

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
)

// publicAssets holds the public site's static files. It is embedded so the build command has no
// dependency on the working directory, and so the bytes in the output are the bytes in the source.
//
//go:embed assets/public.css
var publicAssets embed.FS

// publicAsset returns the content-addressed stylesheet path and bytes.
//
// Hashing the bytes rather than naming the file public.css makes every build's reference and file
// agree even when the stylesheet changes, without a manifest step or a runtime lookup. The hash is
// taken over the exact bytes written, so two builds with the same source cannot disagree.
func publicAsset() (path string, body []byte, err error) {
	css, err := fs.ReadFile(publicAssets, "assets/public.css")
	if err != nil {
		return "", nil, fmt.Errorf("read the embedded public stylesheet: %w", err)
	}

	sum := sha256.Sum256(css)
	name := "assets/public." + hex.EncodeToString(sum[:])[:12] + ".css"
	return "/" + name, css, nil
}
