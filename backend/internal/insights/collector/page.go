package collector

import (
	_ "embed"
	"fmt"
	"html/template"
)

//go:embed summary.html
var pageHTML string

var summaryPage = template.Must(template.New("summary").Funcs(template.FuncMap{
	"percent": func(n, total int) string {
		if total == 0 {
			return "—"
		}
		return fmt.Sprintf("%.1f%%", 100*float64(n)/float64(total))
	},
}).Parse(pageHTML))
