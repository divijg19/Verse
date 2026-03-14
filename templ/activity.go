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

		if _, e := buf.WriteString("<div class=\"grid grid-rows-7 grid-flow-col gap-1.5 w-max\">"); e != nil {
			return e
		}

		for _, d := range days {
			if d.Active {
				if _, e := buf.WriteString("<div class=\"w-3.5 h-3.5 bg-purple-500/80 rounded-[2px] shadow-sm shadow-purple-900/20 transition-all hover:bg-purple-400\"></div>"); e != nil {
					return e
				}
			} else {
				if _, e := buf.WriteString("<div class=\"w-3.5 h-3.5 bg-neutral-800/50 rounded-[2px] transition-colors hover:bg-neutral-700/60\"></div>"); e != nil {
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
