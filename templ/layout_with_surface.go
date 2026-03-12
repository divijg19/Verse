package templ

//lint:file-ignore SA4006 This context is only used if a nested component is present.

import (
	"github.com/a-h/templ"
)

// LayoutWithSurface renders a full page including dynamic navigation for the given surface.
func LayoutWithSurface(surface string, content templ.Component) templ.Component {
	return Layout(surface, content)
}
