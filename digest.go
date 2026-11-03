package guardian

import (
	"context"
	"log"
	"time"
)

// The weekly digest for the parents (criterion 28).

// digestLoop sends the digest every Sunday at 19:00 local time.
func (n *notifier) digestLoop(ctx context.Context) {
	for {
		next := nextDigest(time.Now().In(n.zone))
		t := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
			if err := n.sendDigest(ctx, next); err != nil {
				log.Printf("digest: %v", err)
			}
		}
	}
}

// nextDigest is the first Sunday 19:00 after now.
func nextDigest(now time.Time) time.Time {
	d := time.Date(now.Year(), now.Month(), now.Day(), 19, 0, 0, 0, now.Location())
	for d.Weekday() != time.Sunday || !d.After(now) {
		d = d.AddDate(0, 0, 1)
	}
	return d
}

// sendDigest sends the week up to end to the parents; afterwards the
// child's e-mails that waited for it go out (criterion 25).
func (n *notifier) sendDigest(ctx context.Context, end time.Time) error {
	m := n.mailSettings(ctx)
	if len(m.parents()) == 0 {
		return nil
	}
	end = end.In(n.zone)
	start := end.AddDate(0, 0, -7)
	fs, err := n.st.FindingsSince(ctx, start)
	if err != nil {
		return err
	}
	reqs, err := n.st.HelpRequestsSince(ctx, start)
	if err != nil {
		return err
	}
	sigs, err := n.st.SignalsSince(ctx, start)
	if err != nil {
		return err
	}
	chats, _ := n.st.CountChats(ctx)
	type request struct {
		HelpRequest
		When string
	}
	var rs []request
	for _, r := range reqs {
		rs = append(rs, request{r, n.lang.stamp(r.Created.In(n.zone))})
	}
	html, text, err := n.renderMail("digest", map[string]any{"Findings": fs, "Requests": rs, "Signals": sigs, "Chats": chats,
		"Week": n.lang.T("digest_week", n.lang.day(start), n.lang.day(end)), "Link": n.link("/findings")})
	if err != nil {
		return err
	}
	// The label names every day the digest covers, including today.
	subject := n.lang.T("digest_subject", n.lang.day(start), n.lang.day(end))
	if err := n.sendWith(ctx, m, email{To: m.parents(), Subject: subject, HTML: html, Text: text}); err != nil {
		return err
	}
	ids, err := n.st.ReleaseDigestMails(ctx, time.Now())
	if err != nil {
		return err
	}
	n.sendChildMails(ctx, ids)
	return nil
}
