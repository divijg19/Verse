package tests

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	appserver "github.com/divijg19/Verse/internal/server"
)

// This file exists because /static was served by an http.FileServer over the asset directory, and a
// file server has three properties that are wrong for a directory served to an unauthenticated caller:
//
//   - a request for a directory returns a listing, so /static/, /static/js/ and /static/css/ each
//     described the asset layout to anyone who asked;
//   - everything in the directory was served, which included /static/js/VENDOR.md and
//     VENDOR.sha256, disclosing the vendored library's version, license and digest;
//   - the root came from VERSE_STATIC_DIR and was joined to the request path, so a root of "." --
//     which is a plausible mistake rather than a malicious one -- would have served /static/.env.
//
// internal/server/router.go now serves a fixed list. The tests below assert each of those three
// properties is gone, and the last one asserts the list still describes the directory, because an
// allowlist that quietly falls behind the tree is its own kind of bug: the file gets built, referenced
// in a template, and 404s.

// allowedStaticAssets is the same list the handler serves, restated here rather than imported.
//
// A test that read the handler's own variable would pass if the variable were emptied, which is the
// failure this file exists to catch. Restating it means a change to either side is a test failure, and
// that is the intent.
var allowedStaticAssets = []string{
	"/static/css/output.css",
	"/static/js/editor.js",
	"/static/js/htmx.min.js",
	"/static/js/navigation.js",
}

// explicitlyNotServed are files that live in the asset directory and are deliberately not served.
//
// Declared rather than inferred so that the drift test below has somewhere to record a decision. A
// file that is neither allowed nor listed here is an accident, and the test says so.
//
// Keys are relative to the asset root, matching the paths filepath.Walk reports, so that the two
// halves of the comparison cannot drift apart in how they spell a path.
var explicitlyNotServed = map[string]string{
	"css/input.css":    "a build input, not an asset; publishing it names the framework and version",
	"js/VENDOR.md":     "discloses the vendored library's version and license",
	"js/VENDOR.sha256": "discloses the vendored library's digest",
}

func TestStaticAllowlistServesExactlyWhatIsReferenced(t *testing.T) {
	connectTestDB(t)
	srv := newTestServer(t)

	for _, asset := range allowedStaticAssets {
		status, _, headers := get(t, srv.URL+asset, nil)
		if status != http.StatusOK {
			t.Errorf("GET %s = %d, want 200; the allowlist promises this file, so a 404 here means "+
				"either the file is missing or the list and the templates have diverged", asset, status)
			continue
		}
		if !strings.Contains(headers.Get("Cache-Control"), "max-age=86400") {
			t.Errorf("GET %s missing the cache header, got %q", asset, headers.Get("Cache-Control"))
		}
	}
}

func TestStaticAllowlistServesNothingElse(t *testing.T) {
	connectTestDB(t)
	srv := newTestServer(t)

	// The three exposures, one by one. Each is a specific defect this release closes, so each is
	// named rather than folded into a loop over a list, which would report all three the same way.
	cases := []struct {
		path string
		why  string
	}{
		{"/static/", "a file server returned a directory listing here"},
		{"/static/js/", "a file server returned a directory listing here"},
		{"/static/css/", "a file server returned a directory listing here"},
		{"/static/js/VENDOR.md", "it discloses the vendored library's version and license"},
		{"/static/js/VENDOR.sha256", "it discloses the vendored library's digest"},
		{"/static/css/input.css", "it is a build input, and publishing it names the framework"},
		{"/static/.env", "VERSE_STATIC_DIR pointed at the working directory would have served it"},
		{"/static/../.env", "and so would a traversal, if the root had been the working directory"},
		{"/static/../../etc/passwd", "no request-derived path reaches the filesystem un-matched"},
		{"/static/wasm", "it is an empty leftover directory"},
	}

	for _, tc := range cases {
		status, body, _ := get(t, srv.URL+tc.path, nil)
		if status != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404; %s", tc.path, status, tc.why)
		}
		// A body is not required, but if one comes back it must not be the file. The two 404 handlers
		// in this application are bare, so anything longer means something answered.
		if len(strings.TrimSpace(body)) > 200 {
			t.Errorf("GET %s returned a %d-byte body; expected a bare 404", tc.path, len(body))
		}
	}
}

func TestStaticAllowlistRefusesNonReadMethods(t *testing.T) {
	connectTestDB(t)
	srv := newTestServer(t)

	// An allowlist bounds which files are reachable. It says nothing about what may be done with one,
	// and a static asset has no side effect, so anything other than a read is refused rather than
	// passed to the file server to interpret.
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req, err := http.NewRequestWithContext(t.Context(), method, srv.URL+"/static/js/navigation.js", nil)
		if err != nil {
			t.Fatalf("build %s request: %v", method, err)
		}
		resp, err := authClient.Do(req)
		if err != nil {
			t.Fatalf("%s /static/js/navigation.js: %v", method, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s /static/js/navigation.js = %d, want 405", method, resp.StatusCode)
		}
	}
}

// TestStaticAllowlistMatchesTheAssetDirectory is the drift guard.
//
// An allowlist that falls behind the tree fails quietly: a new stylesheet or script is written, a
// template references it, it 404s in production, and nothing in the build complains. The reverse is
// just as bad -- a file left in the directory after the template that used it was deleted. Both are
// caught by requiring that every file under static/ is either served or explicitly excluded, with a
// stated reason.
//
// Skipped when the generated stylesheet is absent, because a checkout that has never run the Tailwind
// step has output.css missing for a reason that has nothing to do with the allowlist. Asserting
// nothing in that case would be a false pass, so the test says it skipped and why.
func TestStaticAllowlistMatchesTheAssetDirectory(t *testing.T) {
	root := "static"
	if _, err := os.Stat(root); err != nil {
		root = filepath.Join("..", root)
	}
	if _, err := os.Stat(root); err != nil {
		t.Skipf("no static directory found from this working directory: %v", err)
	}

	served := make(map[string]struct{}, len(allowedStaticAssets))
	for _, asset := range allowedStaticAssets {
		served[strings.TrimPrefix(asset, "/static/")] = struct{}{}
	}

	if _, err := os.Stat(filepath.Join(root, "css", "output.css")); err != nil {
		t.Skip("output.css has not been generated; run the Tailwind step to exercise this test fully")
	}

	var unexplained []string
	var emptyDirs []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}

		// A directory is not served and needs no reason of its own, unless it is empty -- an empty
		// directory serves nothing, cannot be requested for a file, and is a leftover that the next
		// person has no way to interpret. static/wasm was one, for as long as this file existed.
		if info.IsDir() {
			entries, readErr := os.ReadDir(path)
			if readErr != nil {
				return readErr
			}
			if len(entries) == 0 {
				emptyDirs = append(emptyDirs, rel)
			}
			return nil
		}

		if _, ok := served[rel]; ok {
			return nil
		}
		if _, ok := explicitlyNotServed[rel]; ok {
			return nil
		}
		unexplained = append(unexplained, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk the asset directory: %v", err)
	}

	sort.Strings(unexplained)
	if len(unexplained) > 0 {
		t.Errorf("these files are in the asset directory and are neither served nor explicitly "+
			"excluded:\n  %s\nEither add the file to staticAssets in internal/server/router.go, or "+
			"record why it is not served, in explicitlyNotServed in this file. Silently serving it "+
			"is how VENDOR.md became public.",
			strings.Join(unexplained, "\n  "))
	}

	sort.Strings(emptyDirs)
	for _, dir := range emptyDirs {
		t.Errorf("%s is an empty directory in the asset directory; it serves nothing and is a "+
			"leftover with no way to interpret it. Delete it.", dir)
	}

	// And the reverse: a listed asset with no file behind it, which is a 404 waiting for a deploy.
	for asset := range served {
		if _, err := os.Stat(filepath.Join(root, asset)); err != nil {
			t.Errorf("staticAssets lists %q but there is no such file: %v", asset, err)
		}
	}
}

// TestAMisconfiguredAssetRootFailsLoudly covers the one thing about the allowlist that is not a
// request-time property: that a deployment which points the variable somewhere unusable says so at
// startup rather than serving a 404 for every asset.
//
// The default root is deliberately not covered by this, and should not be. "static" resolves against
// the working directory, so its absence means the process was started from somewhere other than the
// project root, which is a normal thing to do with a built binary. Several tests here construct a
// router from a directory with no static/ tree, and an earlier version of this check refused to build
// one at all.
func TestAMisconfiguredAssetRootFailsLoudly(t *testing.T) {
	t.Setenv("VERSE_AUTHORIZATION", testPassphrase)
	t.Setenv("VERSE_AUTH_SECRET", testAuthSecret)
	t.Setenv("VERSE_E2E_DATABASE_URL", "postgres://verse:verse@127.0.0.1:5433/verse_test?sslmode=disable")
	t.Setenv("VERSE_E2E_ALLOW_DESTRUCTIVE", "1")

	// A path that does not exist.
	t.Setenv("VERSE_STATIC_DIR", filepath.Join(t.TempDir(), "no-such-directory"))
	_, err := appserver.NewRouter()
	if err == nil {
		t.Fatal("NewRouter accepted an asset root that does not exist")
	}
	if !strings.Contains(err.Error(), "VERSE_STATIC_DIR") {
		t.Errorf("error does not name the variable that is wrong: %v", err)
	}

	// A path that exists but is a file. Caught separately because the failure mode differs: stat
	// succeeds, and it is IsDir that has to notice.
	notADir := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(notADir, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write the decoy file: %v", err)
	}
	t.Setenv("VERSE_STATIC_DIR", notADir)
	_, err = appserver.NewRouter()
	if err == nil {
		t.Fatal("NewRouter accepted an asset root that is a file")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("error does not say the root is a file: %v", err)
	}
}
