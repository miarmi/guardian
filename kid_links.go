package guardian

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Signed links for the child, the only pages reachable without the lab
// login. A finding's link (criterion 26) signs the finding id; the personal
// help link (criterion 30) signs a generation number, so sending a new one
// retires the old. Each kind has its own MAC domain, so one can't stand in
// for the other.

const (
	tokenFinding = "finding"
	tokenHelp    = "help"

	kidLinkLife  = 30 * 24 * time.Hour
	helpLinkLife = 365 * 24 * time.Hour
)

// signingKey loads or makes the key behind the child's links. It is read
// once and kept, so two first uses at the same moment can't each write a
// different key and invalidate the other's link.
func (n *notifier) signingKey() ([]byte, error) {
	n.keyMu.Lock()
	defer n.keyMu.Unlock()
	if n.kidKey != nil {
		return n.kidKey, nil
	}
	path := filepath.Join(n.secretsDir, "kid-key")
	if b, err := os.ReadFile(path); err == nil && len(b) >= 32 {
		n.kidKey = b
		return b, nil
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	if err := writeSecret(n.secretsDir, "kid-key", string(b)); err != nil {
		return nil, err
	}
	n.kidKey = b
	return b, nil
}

// sign makes a token: base64(id, expiry) "." base64(MAC).
func (n *notifier) sign(kind string, id int64, expires time.Time) (string, error) {
	key, err := n.signingKey()
	if err != nil {
		return "", err
	}
	payload := make([]byte, 16)
	binary.BigEndian.PutUint64(payload[:8], uint64(id))
	binary.BigEndian.PutUint64(payload[8:], uint64(expires.Unix()))
	return b64.EncodeToString(payload) + "." + b64.EncodeToString(tokenMAC(key, kind, payload)), nil
}

func tokenMAC(key []byte, kind string, payload []byte) []byte {
	mac := hmac.New(sha256.New, key)
	if kind != tokenFinding { // finding links keep the original form
		mac.Write([]byte(kind + ":"))
	}
	mac.Write(payload)
	return mac.Sum(nil)[:16]
}

// check returns the id of a valid, unexpired token of that kind.
func (n *notifier) check(kind, token string) (int64, bool) {
	p, sig, ok := strings.Cut(token, ".")
	if !ok {
		return 0, false
	}
	payload, err1 := b64.DecodeString(p)
	got, err2 := b64.DecodeString(sig)
	if err1 != nil || err2 != nil || len(payload) != 16 || len(got) != 16 {
		return 0, false
	}
	// Only the exact spelling we issued counts: the last character of each
	// part carries spare bits, and a changed one must not decode the same.
	if b64.EncodeToString(payload) != p || b64.EncodeToString(got) != sig {
		return 0, false
	}
	key, err := n.signingKey()
	if err != nil || !hmac.Equal(tokenMAC(key, kind, payload), got) {
		return 0, false
	}
	if time.Now().Unix() > int64(binary.BigEndian.Uint64(payload[8:])) {
		return 0, false
	}
	return int64(binary.BigEndian.Uint64(payload[:8])), true
}

// findingLink is the child's link to one finding.
func (n *notifier) findingLink(findingID int64) (string, error) {
	token, err := n.sign(tokenFinding, findingID, time.Now().Add(kidLinkLife))
	if err != nil {
		return "", err
	}
	return n.link("/kid/" + token), nil
}

func (n *notifier) checkFindingToken(token string) (int64, bool) {
	return n.check(tokenFinding, token)
}

// --- the personal help link (criterion 30) ---

// helpGeneration is the current help link's number and expiry; 0 when none
// was ever made.
func (n *notifier) helpGeneration(ctx context.Context) (gen int64, until time.Time) {
	v := n.st.Settings(ctx, "help_link_gen", "help_link_expires")
	gen, _ = strconv.ParseInt(v["help_link_gen"], 10, 64)
	exp, _ := strconv.ParseInt(v["help_link_expires"], 10, 64)
	return gen, time.Unix(exp, 0)
}

func (n *notifier) saveHelpGeneration(ctx context.Context, gen int64, until time.Time) error {
	if err := n.st.SetSetting(ctx, "help_link_gen", strconv.FormatInt(gen, 10)); err != nil {
		return err
	}
	return n.st.SetSetting(ctx, "help_link_expires", strconv.FormatInt(until.Unix(), 10))
}

func (n *notifier) helpLinkFor(gen int64, until time.Time) (string, error) {
	token, err := n.sign(tokenHelp, gen, until)
	if err != nil {
		return "", err
	}
	return n.link("/kid/help/" + token), nil
}

// helpLink returns the child's current personal link and until when it is
// valid; with create, a missing or expired one is made (without e-mail).
func (n *notifier) helpLink(ctx context.Context, create bool) (string, time.Time, error) {
	gen, until := n.helpGeneration(ctx)
	if gen == 0 || !time.Now().Before(until) {
		if !create {
			return "", time.Time{}, nil
		}
		gen, until = gen+1, time.Now().Add(helpLinkLife)
		if err := n.saveHelpGeneration(ctx, gen, until); err != nil {
			return "", time.Time{}, err
		}
	}
	link, err := n.helpLinkFor(gen, until)
	if err != nil {
		return "", time.Time{}, err
	}
	return link, until, nil
}

// checkHelpToken accepts only the current generation's link.
func (n *notifier) checkHelpToken(ctx context.Context, token string) bool {
	gen, ok := n.check(tokenHelp, token)
	if !ok {
		return false
	}
	cur, _ := n.helpGeneration(ctx)
	return gen == cur
}

// sendHelpLink makes a new personal link and e-mails it to the child; the
// old link stops working only once the new one went out.
func (n *notifier) sendHelpLink(ctx context.Context) error {
	m := n.mailSettings(ctx)
	if m.ChildEmail == "" {
		return errors.New(n.lang.T("mail_no_child"))
	}
	gen, _ := n.helpGeneration(ctx)
	gen++
	until := time.Now().Add(helpLinkLife)
	link, err := n.helpLinkFor(gen, until)
	if err != nil {
		return err
	}
	html, text, err := n.renderMail("helplink", map[string]any{"Link": link, "Contacts": n.lang.contacts, "Until": n.lang.date(until.In(n.zone))})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	if err := n.sendWith(ctx, m, email{To: []string{m.ChildEmail}, Subject: n.lang.T("helplink_subject"), HTML: html, Text: text, Links: []string{link}}); err != nil {
		return err
	}
	return n.saveHelpGeneration(context.WithoutCancel(ctx), gen, until)
}
