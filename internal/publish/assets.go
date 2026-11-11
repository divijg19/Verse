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
//go:embed assets/public.css assets/public.js
var publicAssets embed.FS

// publicAsset returns the content-addressed path and bytes of one embedded asset.
//
// Hashing the bytes rather than naming the file public.css makes every build's reference and file
// agree even when the stylesheet changes, without a manifest step or a runtime lookup. The hash is
// taken over the exact bytes written, so two builds with the same source cannot disagree.
//
// The name is the same in both cases -- "public" -- and the extension is what tells them apart, so
// the stylesheet and the script sit beside each other in the output.
func publicAsset(name string) (path string, body []byte, err error) {
	bytes, err := fs.ReadFile(publicAssets, "assets/"+name)
	if err != nil {
		return "", nil, fmt.Errorf("read the embedded public %s: %w", name, err)
	}

	sum := sha256.Sum256(bytes)
	extension := "css"
	if len(name) > 3 && name[len(name)-3:] == ".js" {
		extension = "js"
	}

	return "/assets/public." + hex.EncodeToString(sum[:])[:12] + "." + extension, bytes, nil
}

// publicStylesheet returns the public site's stylesheet.
func publicStylesheet() (string, []byte, error) {
	return publicAsset("public.css")
}

// publicScript returns the public site's script.
//
// It is the archive's search box, and it is the only JavaScript the public site ships. The site
// works without it: the archive page is the complete list of published work whether the script
// loads or not, and the search box is inserted by the script rather than being in the markup.
//
// The bundle is built from web/ts/public_search.ts by scripts/build_js.ts and committed, so the
// publish binary needs no TypeScript toolchain and the output is byte-stable.
func publicScript() (string, []byte, error) {
	return publicAsset("public.js")
}
