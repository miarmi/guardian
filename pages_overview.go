package guardian

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The overview: the connector, the analysis, banners and the chat list.

type indexData struct {
	page
	Preview   string // preview name, "" in production and development
	Connector connectorView
	Chats     []ChatSummary
	Analysis  overviewAnalysis
}

func (a *App) indexPage(w http.ResponseWriter, r *http.Request) {
	chats, err := a.store.Chats(r.Context())
	if err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	labelSameNames(chats)
	a.render(w, "index", http.StatusOK, indexData{
		page: a.page(), Preview: a.preview, Connector: a.connector(), Chats: chats,
		Analysis: a.overviewAnalysis(r.Context()),
	})
}

// labelSameNames tells chats with the same name apart: a direct chat by the
// end of its number, others by "(1)", "(2)"… in the order they first
// appeared, so a label never moves to another chat.
func labelSameNames(chats []ChatSummary) {
	firsts := map[string][]int64{}
	for _, c := range chats {
		if c.Name != "" {
			firsts[c.Name] = append(firsts[c.Name], c.First)
		}
	}
	for _, f := range firsts {
		slices.Sort(f)
	}
	for i, c := range chats {
		same := firsts[c.Name]
		if c.Name == "" || len(same) < 2 {
			continue
		}
		user, _, _ := strings.Cut(c.JID, "@")
		if !c.IsGroup && len(user) > 3 && strings.Trim(user, "0123456789") == "" {
			chats[i].Suffix = "…" + user[len(user)-3:]
			continue
		}
		if n := slices.Index(same, c.First); n >= 0 {
			chats[i].Suffix = "(" + strconv.Itoa(n+1) + ")"
		}
	}
}

// connectorView is the WhatsApp row of the "Connectors" list.
type connectorView struct {
	Available  bool // false in previews: nothing to press
	StateText  string
	Note       string // one line of help under the state, if any
	CanConnect bool
	Connected  bool
}

func (a *App) connector() connectorView {
	if a.wa == nil {
		return connectorView{StateText: a.lang.T("not_previews")}
	}
	s := a.wa.Status()
	switch s.State {
	case waConnected:
		return connectorView{Available: true, StateText: a.lang.T("st_as", formatNumber(s.Number)), Connected: true}
	case waPhoneRemoved:
		return connectorView{Available: true, StateText: a.lang.T("st_removed"), Note: a.lang.T("removed_next"), CanConnect: true}
	default: // not connected, or a code on offer
		return connectorView{Available: true, StateText: a.lang.T("st_not"), CanConnect: true}
	}
}

// formatNumber groups a phone number the way people write it. Czech and
// Slovak numbers get the usual 3-3-3 grouping; others stay as they are.
func formatNumber(digits string) string {
	if (strings.HasPrefix(digits, "420") || strings.HasPrefix(digits, "421")) && len(digits) == 12 {
		return "+" + digits[:3] + " " + digits[3:6] + " " + digits[6:9] + " " + digits[9:]
	}
	return "+" + digits
}

// overviewAnalysis is what the overview says about the analysis and the
// notifications (criteria 12, 13, 17–20, 25).
type overviewAnalysis struct {
	Available   bool // false in previews: no analysis runs there
	State       analysisState
	Destination string // host the text goes to, "" when it stays home
	OpenCount   int
	Banners     []banner // what waits for the parents, the child's request first
	Reason      string   // why there is no model, when it is just the key
	MailFailed  string   // why the child's e-mail didn't go out (criterion 25)
	GmailFailed string   // why Gmail couldn't be read (criterion 35)
	SignalsWeek int      // signals from parental e-mails in the last 7 days
}

func (a *App) overviewAnalysis(ctx context.Context) overviewAnalysis {
	o := overviewAnalysis{MailFailed: a.nt.childMailError(ctx)}
	if a.an != nil {
		o.GmailFailed = a.an.gmailError()
	}
	if sigs, err := a.store.SignalsSince(ctx, time.Now().AddDate(0, 0, -7)); err == nil {
		o.SignalsWeek = len(sigs)
	}
	o.OpenCount, _ = a.store.OpenFindingCount(ctx)
	o.Banners = a.banners(ctx)
	if a.an == nil {
		o.State = analysisState{NoModel: true}
		return o
	}
	o.Available = true
	m := a.an.settings(ctx)
	o.State = a.an.state(m)
	if !o.State.NoModel {
		o.Destination = m.destination()
	} else if m.Type == modelAnthropic && m.Key == "" {
		o.Reason = a.lang.T("no_key_reason")
	}
	return o
}

// banner is one notice at the top of the overview. The child's requests
// (honey) and a new serious finding (red) each get their own, so neither
// hides the other.
type banner struct {
	Text  string
	More  string // "+1 další" when another of the same kind waits
	Child bool
}

func (a *App) banners(ctx context.Context) []banner {
	var out []banner
	var child []string
	if sit, chat, ok, _ := a.store.UnseenHelp(ctx); ok {
		child = append(child, a.lang.T("help_banner", sit, chat))
	}
	if ok, _ := a.store.UnseenHelpRequest(ctx); ok {
		child = append(child, a.lang.T("help_request"))
	}
	if len(child) > 0 {
		b := banner{Text: child[0], Child: true}
		if len(child) > 1 {
			b.More = a.lang.T("banner_more", len(child)-1)
		}
		out = append(out, b)
	}
	if sit, chat, ok, _ := a.store.UnseenAlert(ctx); ok {
		out = append(out, banner{Text: a.lang.T("alert_banner", sit, chat)})
	}
	return out
}
