package guardian

import (
	htmltemplate "html/template"
	"io"
	"strings"
	texttemplate "text/template"
)

// mailTemplates renders the .html parts with HTML escaping and the .txt
// parts as plain text.
type mailTemplates struct {
	html *htmltemplate.Template
	text *texttemplate.Template
}

func (m mailTemplates) ExecuteTemplate(w io.Writer, name string, data any) error {
	if strings.HasSuffix(name, ".txt") {
		return m.text.ExecuteTemplate(w, name, data)
	}
	return m.html.ExecuteTemplate(w, name, data)
}

var mailTmpl = mailTemplates{
	html: htmltemplate.Must(htmltemplate.New("").Funcs(htmltemplate.FuncMap{"linkify": linkify}).ParseFS(webFS, "web/mail/*.html")),
	text: texttemplate.Must(texttemplate.ParseFS(webFS, "web/mail/*.txt")),
}
