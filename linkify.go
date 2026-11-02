package guardian

import (
	"html/template"
	"regexp"
	"strings"
)

// linkify makes the help contacts tappable (criterion 27): phone numbers
// become tel: links, web addresses https: links that never break mid-name.
// Everything else is escaped. The inline style keeps that in e-mails too.
var contactToken = regexp.MustCompile(`\b(116 \d{3}|158|155|150|112)\b|\b([a-z0-9-]+\.(?:cz|sk|org|com|eu))\b`)

func linkify(s string) template.HTML {
	var b strings.Builder
	last := 0
	for _, m := range contactToken.FindAllStringSubmatchIndex(s, -1) {
		b.WriteString(template.HTMLEscapeString(s[last:m[0]]))
		tok := s[m[0]:m[1]]
		if m[2] >= 0 {
			b.WriteString(`<a class="tel" href="tel:` + strings.ReplaceAll(tok, " ", "") + `" style="white-space:nowrap">` + template.HTMLEscapeString(tok) + `</a>`)
		} else {
			b.WriteString(`<a class="site" href="https://` + tok + `" style="white-space:nowrap">` + template.HTMLEscapeString(tok) + `</a>`)
		}
		last = m[1]
	}
	b.WriteString(template.HTMLEscapeString(s[last:]))
	return template.HTML(b.String())
}
