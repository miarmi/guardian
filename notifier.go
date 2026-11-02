package guardian

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"errors"
	"log"
	"strings"
	"sync"
	"time"
)

// Phase 3: telling people (criteria 22–30). The notifier's work is spread
// over mail_send.go, mail_settings.go, kid_links.go, child_mail.go and
// digest.go; this file holds what they share and the pushes to the parents.

// notifier sends pushes and e-mails, makes the child's links and the digest.
type notifier struct {
	st         *store
	lang       *lang
	zone       *time.Location
	secretsDir string
	baseURL    string
	practice   bool

	mu       sync.Mutex
	outbox   []outboxItem // practice mode: what was "sent", oldest first
	mailDown bool         // practice mode: sending fails
	graph    graphToken

	// keyMu guards the keys kept in secrets/. It is separate from mu so
	// reading or making a key never holds up the outbox or the mail token.
	keyMu  sync.Mutex
	vapid  *ecdsa.PrivateKey
	kidKey []byte
}

// outboxItem is something "sent" in practice mode (criterion 29).
type outboxItem struct {
	Kind    string    `json:"kind"` // email or push
	To      string    `json:"to"`
	Subject string    `json:"subject"`
	Text    string    `json:"text"`
	Links   []string  `json:"links"`
	At      time.Time `json:"at"`
	HTML    string    `json:"html,omitempty"` // e-mails: how they look
}

func (n *notifier) addToOutbox(item outboxItem) {
	n.mu.Lock()
	n.outbox = append(n.outbox, item)
	n.mu.Unlock()
}

// setting reads one saved value; an unreadable one reads as "".
func (n *notifier) setting(ctx context.Context, key string) string {
	v, _ := n.st.Setting(ctx, key)
	return v
}

// link makes an absolute address for e-mails and pushes.
func (n *notifier) link(path string) string {
	return strings.TrimRight(n.baseURL, "/") + path
}

// renderMail fills web/mail/<name>.html and <name>.txt; the templates get
// the page language as L.
func (n *notifier) renderMail(name string, data map[string]any) (html, text string, err error) {
	data["L"] = n.lang
	var h, t bytes.Buffer
	if err := mailTmpl.ExecuteTemplate(&h, name+".html", data); err != nil {
		return "", "", err
	}
	if err := mailTmpl.ExecuteTemplate(&t, name+".txt", data); err != nil {
		return "", "", err
	}
	return h.String(), t.String(), nil
}

// --- pushes to the parents (criterion 22) ---

func (n *notifier) vapidKey() (*ecdsa.PrivateKey, error) {
	n.keyMu.Lock()
	defer n.keyMu.Unlock()
	if n.vapid != nil {
		return n.vapid, nil
	}
	k, err := loadVAPIDKey(n.secretsDir)
	if err == nil {
		n.vapid = k
	}
	return k, err
}

// pushParents tells every subscribed device; in practice mode the push goes
// to the outbox instead.
func (n *notifier) pushParents(ctx context.Context, m pushMessage) {
	if n.practice {
		n.addToOutbox(outboxItem{Kind: "push", To: "parents", Subject: m.Title, Text: m.Body, Links: []string{m.URL}, At: time.Now()})
		return
	}
	devices, err := n.st.PushDevices(ctx)
	if err != nil || len(devices) == 0 {
		return
	}
	key, err := n.vapidKey()
	if err != nil {
		log.Printf("push: %v", err)
		return
	}
	contact := "mailto:guardian@localhost"
	if ps := n.mailSettings(ctx).parents(); len(ps) > 0 {
		contact = "mailto:" + ps[0]
	}
	for _, d := range devices {
		err := sendPush(ctx, key, contact, d, m)
		switch {
		case errors.Is(err, errGone):
			if err := n.st.RemovePushEndpoint(ctx, d.Endpoint); err != nil {
				log.Printf("push: forgetting %s: %v", d.Label, err)
			}
		case err != nil:
			log.Printf("push to %s: %v", d.Label, err)
		}
	}
}

// --- what happens on a finding or a request for help ---

// onFinding tells the parents about a new finding first (criterion 24) and
// queues the child's e-mail for after them (criterion 25).
func (n *notifier) onFinding(ctx context.Context, id int64, isNew bool) {
	if !isNew {
		return
	}
	f, ok, err := n.st.Finding(ctx, id)
	if err != nil || !ok {
		return
	}
	due := time.Time{} // after the parents' digest
	if f.Situation.Action == "alert" {
		n.pushParents(ctx, pushMessage{
			Title: n.lang.T("push_title", f.Situation.Name),
			Body:  n.lang.T("push_body", f.ChatLabel()),
			URL:   n.link("/findings"),
		})
		due = time.Now() // at the next hourly look after this one
	}
	if err := n.st.QueueChildMail(ctx, f.ID, due); err != nil {
		log.Printf("queueing the child's e-mail: %v", err)
	}
}

// onHelp tells the parents the child asked for help with a finding (criterion 26).
func (n *notifier) onHelp(ctx context.Context, f Finding) {
	n.pushParents(ctx, pushMessage{Title: "Guardian", Body: n.lang.T("help_banner", f.Situation.Name, f.ChatLabel()), URL: n.link("/findings")})
}

// onHelpRequest tells the parents the child asked for help through the
// personal link (criterion 30); never with the child's message.
func (n *notifier) onHelpRequest(ctx context.Context) {
	n.pushParents(ctx, pushMessage{Title: "Guardian", Body: n.lang.T("help_request"), URL: n.link("/findings")})
}

// testPush sends a test notification to every device.
func (n *notifier) testPush(ctx context.Context) {
	n.pushParents(ctx, pushMessage{Title: "Guardian", Body: n.lang.T("push_test"), URL: n.link("/settings")})
}
