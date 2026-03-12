package templ

// Small helpers used by generated templ code.

import (
	"github.com/a-h/templ"
	templruntime "github.com/a-h/templ/runtime"
)

// If renders its children only when cond is true.
func If(cond bool) templ.Component {
	return templruntime.GeneratedTemplate(func(input templruntime.GeneratedComponentInput) (err error) {
		w, ctx := input.Writer, input.Context
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		ctx = templ.InitializeContext(ctx)
		if !cond {
			return nil
		}
		child := templ.GetChildren(ctx)
		if child == nil {
			return nil
		}
		return child.Render(ctx, w)
	})
}

// Children returns a component that renders the captured children.
func Children() templ.Component {
	return templruntime.GeneratedTemplate(func(input templruntime.GeneratedComponentInput) (err error) {
		w, ctx := input.Writer, input.Context
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		ctx = templ.InitializeContext(ctx)
		child := templ.GetChildren(ctx)
		if child == nil {
			return nil
		}
		return child.Render(ctx, w)
	})
}
