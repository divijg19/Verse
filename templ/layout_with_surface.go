package templ

//lint:file-ignore SA4006 This context is only used if a nested component is present.

import (
	"github.com/a-h/templ"
	templruntime "github.com/a-h/templ/runtime"
)

// LayoutWithSurface renders a full page including dynamic navigation for the given surface.
func LayoutWithSurface(surface string, content templ.Component) templ.Component {
	return templruntime.GeneratedTemplate(func(input templruntime.GeneratedComponentInput) (templErr error) {
		w, ctx := input.Writer, input.Context
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		buf, isBuffer := templruntime.GetBuffer(w)
		if !isBuffer {
			defer func() {
				bufErr := templruntime.ReleaseBuffer(buf)
				if templErr == nil {
					templErr = bufErr
				}
			}()
		}

		templruntime.WriteString(buf, 1, "<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\"><title>Verse</title><link rel=\"stylesheet\" href=\"/static/css/output.css\"><script src=\"https://unpkg.com/htmx.org\"></script><style>\n\t/* Fixed edge navigation positions */\n\t#nav-top { position: fixed; top: 24px; left: 50%; transform: translateX(-50%); }\n\t#nav-left { position: fixed; left: 24px; top: 50%; transform: translateY(-50%); }\n\t#nav-right { position: fixed; right: 24px; top: 50%; transform: translateY(-50%); }\n\t#nav-bottom { position: fixed; bottom: 24px; left: 50%; transform: translateX(-50%); }\n</style></head><body class=\"bg-neutral-950 text-neutral-200 min-h-screen\"><div id=\"viewport\" class=\"min-h-screen flex items-center justify-center\">")

		// Render dynamic nav (renders nav-top/left/right/bottom divs)
		if err := Nav(surface).Render(ctx, buf); err != nil {
			return err
		}

		templruntime.WriteString(buf, 2, "<div id=\"screen\" class=\"max-w-3xl w-full transition-all duration-200 ease-out p-8\">")

		if content != nil {
			if err := content.Render(ctx, buf); err != nil {
				return err
			}
		}

		templruntime.WriteString(buf, 3, "</div></div><script src=\"/static/js/navigation.js\"></script></body></html>")

		return nil
	})
}

var _ = templruntime.GeneratedTemplate
