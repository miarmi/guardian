package guardian

import (
	"context"
	"log"
	"time"
)

// The child hears about a finding by e-mail, always after the parents
// (criterion 25): an alert's e-mail goes out at the next hourly look, a
// digest situation's after the weekly digest. store_findings.go keeps the
// queue.

// retryChildMails runs after every hourly look: it sends the child's
// e-mails whose parents were told before the look began, and tries failed
// ones again.
func (n *notifier) retryChildMails(ctx context.Context, lookStart time.Time) {
	ids, err := n.st.ChildMailsDue(ctx, lookStart)
	if err != nil {
		log.Printf("mail to the child: %v", err)
		return
	}
	n.sendChildMails(ctx, ids)
}

// sendChildMails sends each e-mail; a failure is kept, shown on the
// overview and tried again at the next look.
func (n *notifier) sendChildMails(ctx context.Context, ids []int64) {
	if len(ids) == 0 {
		return
	}
	m := n.mailSettings(ctx)
	failed := false
	for _, id := range ids {
		f, ok, err := n.st.Finding(ctx, id)
		if err != nil || !ok || f.Status == "false_alarm" {
			n.st.ChildMailSent(ctx, id) // nothing to send
			continue
		}
		if err := n.mailChild(ctx, m, f); err != nil {
			log.Printf("mail to the child: %v", err)
			n.st.SetSetting(ctx, "mail_child_error", n.lang.explain(err))
			failed = true
			continue
		}
		n.st.ChildMailSent(ctx, id)
	}
	if !failed {
		n.st.SetSetting(ctx, "mail_child_error", "")
	}
}

// childMailError is why the child's e-mail last failed, "" when all went out.
func (n *notifier) childMailError(ctx context.Context) string {
	return n.setting(ctx, "mail_child_error")
}

// mailChild sends the e-mail about one finding, with the link to it and the
// personal help link. Without the child's address there is nothing to do.
func (n *notifier) mailChild(ctx context.Context, m mailSettings, f Finding) error {
	if m.ChildEmail == "" {
		return nil
	}
	link, err := n.findingLink(f.ID)
	if err != nil {
		return err
	}
	help, _, err := n.helpLink(ctx, true)
	if err != nil {
		return err
	}
	html, text, err := n.renderMail("child", map[string]any{"F": f, "Link": link, "HelpLink": help, "Contacts": n.lang.contacts})
	if err != nil {
		return err
	}
	return n.sendWith(ctx, m, email{To: []string{m.ChildEmail}, Subject: n.lang.T("child_subject"), HTML: html, Text: text, Links: []string{link, help}})
}
