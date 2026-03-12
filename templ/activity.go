package templ

import (
	"github.com/a-h/templ"
	templruntime "github.com/a-h/templ/runtime"
)

// ActivityGrid is a hand-written component that renders a 30-day grid of activity squares.
func ActivityGrid(days []DayActivity) templ.Component {
	return templruntime.GeneratedTemplate(func(input templruntime.GeneratedComponentInput) (err error) {
		w, ctx := input.Writer, input.Context
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		buf, isBuffer := templruntime.GetBuffer(w)
		if !isBuffer {
			defer func() {
				bufErr := templruntime.ReleaseBuffer(buf)
				if err == nil {
					err = bufErr
				}
			}()
		}

		if _, e := buf.WriteString("<div class=\"grid grid-cols-7 gap-1\">"); e != nil {
			return e
		}

		for _, d := range days {
			if d.Active {
				if _, e := buf.WriteString("<div class=\"w-6 h-6 bg-purple-600 rounded\"></div>"); e != nil {
					return e
				}
			} else {
				if _, e := buf.WriteString("<div class=\"w-6 h-6 bg-neutral-800 rounded\"></div>"); e != nil {
					return e
				}
			}
		}

		if _, e := buf.WriteString("</div>"); e != nil {
			return e
		}

		return nil
	})
}
