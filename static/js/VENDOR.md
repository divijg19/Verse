# Vendored third-party assets

Files in this directory that are committed to the repository and served to the authoring
application. Keep this file up to date whenever a vendored asset is added, removed, or upgraded.

## `htmx.min.js`

| | |
|---|---|
| **Version** | 2.0.4 |
| **Upstream** | https://unpkg.com/htmx.org@2.0.4/dist/htmx.min.js |
| **License** | BSD-2-Clause (https://github.com/bigskysoftware/htmx) |
| **SHA-256** | `e209dda5c8235479f3166defc7750e1dbcd5a5c1808b7792fc2e6733768fb447` |
| **Size** | 50917 bytes |
| **Fetched** | 2026-09-26 |

### Why vendored rather than loaded from a CDN

`templ/layout.templ` previously loaded htmx from `https://unpkg.com/htmx.org` with **no version
constraint**, which means every authoring page load fetched whatever was latest at that moment and
executed it in the origin that holds the authoring session. Three concrete problems:

1. **No verifiable version.** There was nothing to integrity-check and nothing to roll back to.
2. **Supply-chain exposure.** A compromise of the CDN executes arbitrary JavaScript in the authoring
   origin.
3. **Third-party request on every page load** from an application whose entire premise is privacy,
   leaking the author's IP address, user agent, and referrer.

Vendoring removes all three. The authoring application now makes no third-party network requests.

### Upgrade procedure

```bash
VER=2.0.5   # version being upgraded to
curl -fsSL "https://unpkg.com/htmx.org@${VER}/dist/htmx.min.js" -o static/js/htmx.min.js
sha256sum static/js/htmx.min.js
templ generate && go test ./tests/ -count=1
```

Update the table above with the new version, hash, and date in the same commit. Never upgrade htmx
and change templates in the same commit, so a regression is attributable.
