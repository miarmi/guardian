package guardian

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// A tiny, read-only IMAP client: just enough to ask a mailbox for the
// e-mails of a few senders (criterion 33). It opens the inbox with EXAMINE
// (read-only) and fetches with BODY.PEEK, so nothing is ever changed, moved
// or marked as read. Standard library only.

const imapTimeout = 60 * time.Second

type imapConn struct {
	conn net.Conn
	r    *bufio.Reader
	tag  int
}

// imapDial connects with TLS from the start (port 993) and signs in.
func imapDial(ctx context.Context, addr, user, password string, cfg *tls.Config) (*imapConn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		cfg = &tls.Config{ServerName: host}
	}
	d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 20 * time.Second}, Config: cfg}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	c := &imapConn{conn: conn, r: bufio.NewReader(conn)}
	c.deadline(ctx)
	greeting, err := c.line()
	if err != nil {
		conn.Close()
		return nil, err
	}
	if !strings.HasPrefix(greeting, "* OK") {
		conn.Close()
		return nil, fmt.Errorf("unexpected IMAP greeting: %s", clip(greeting, 80))
	}
	if _, err := c.cmd(ctx, "LOGIN "+imapQuote(user)+" "+imapQuote(password)); err != nil {
		conn.Close()
		return nil, err
	}
	return c, nil
}

func (c *imapConn) deadline(ctx context.Context) {
	d := time.Now().Add(imapTimeout)
	if t, ok := ctx.Deadline(); ok && t.Before(d) {
		d = t
	}
	c.conn.SetDeadline(d)
}

func (c *imapConn) Close() {
	c.cmd(context.Background(), "LOGOUT")
	c.conn.Close()
}

// imapResp is one untagged response line, with any literals it carried.
type imapResp struct {
	Line     string
	Literals [][]byte
}

// cmd sends a command and reads until its tagged answer; a NO or BAD answer
// is an error carrying the server's words.
func (c *imapConn) cmd(ctx context.Context, command string) ([]imapResp, error) {
	c.deadline(ctx)
	c.tag++
	tag := "g" + strconv.Itoa(c.tag)
	if _, err := io.WriteString(c.conn, tag+" "+command+"\r\n"); err != nil {
		return nil, err
	}
	var out []imapResp
	for {
		resp, err := c.response()
		if err != nil {
			return nil, err
		}
		if rest, ok := strings.CutPrefix(resp.Line, tag+" "); ok {
			if strings.HasPrefix(rest, "OK") {
				return out, nil
			}
			return nil, errors.New("IMAP: " + clip(strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(rest, "NO"), "BAD")), 160))
		}
		out = append(out, resp)
	}
}

var literalTail = regexp.MustCompile(`\{(\d+)\}$`)

// maxLiteral bounds one literal, so a huge e-mail can't exhaust memory.
const maxLiteral = 4 << 20

// response reads one logical line; a line ending in {n} is followed by n
// bytes of literal data and then the rest of the line.
func (c *imapConn) response() (imapResp, error) {
	var resp imapResp
	for {
		line, err := c.line()
		if err != nil {
			return resp, err
		}
		resp.Line += line
		m := literalTail.FindStringSubmatch(line)
		if m == nil {
			return resp, nil
		}
		n, _ := strconv.Atoi(m[1])
		if n > maxLiteral {
			return resp, errors.New("IMAP: message too large")
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(c.r, buf); err != nil {
			return resp, err
		}
		resp.Literals = append(resp.Literals, buf)
	}
}

func (c *imapConn) line() (string, error) {
	s, err := c.r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(s, "\r\n"), nil
}

// examine opens a mailbox read-only and returns its UIDVALIDITY.
func (c *imapConn) examine(ctx context.Context, box string) (uint32, error) {
	resps, err := c.cmd(ctx, "EXAMINE "+imapQuote(box))
	if err != nil {
		return 0, err
	}
	for _, r := range resps {
		if i := strings.Index(r.Line, "[UIDVALIDITY "); i >= 0 {
			v := r.Line[i+len("[UIDVALIDITY "):]
			if j := strings.IndexByte(v, ']'); j > 0 {
				n, _ := strconv.ParseUint(v[:j], 10, 32)
				return uint32(n), nil
			}
		}
	}
	return 0, nil
}

// searchFrom returns the UIDs above after (or since the date, on a first
// run) whose From contains one of the domains. The search runs on the
// server: nothing about other e-mails is downloaded.
func (c *imapConn) searchFrom(ctx context.Context, domains []string, after uint32, since time.Time) ([]uint32, error) {
	var crit string
	for i, d := range domains {
		term := "FROM " + imapQuote(d)
		if i == 0 {
			crit = term
		} else {
			crit = "OR " + crit + " " + term
		}
	}
	q := "UID SEARCH UID " + strconv.FormatUint(uint64(after)+1, 10) + ":* " + crit
	if after == 0 {
		q += " SINCE " + since.Format("2-Jan-2006")
	}
	resps, err := c.cmd(ctx, q)
	if err != nil {
		return nil, err
	}
	var uids []uint32
	for _, r := range resps {
		rest, ok := strings.CutPrefix(r.Line, "* SEARCH")
		if !ok {
			continue
		}
		for _, f := range strings.Fields(rest) {
			if n, err := strconv.ParseUint(f, 10, 32); err == nil && uint32(n) > after {
				uids = append(uids, uint32(n))
			}
		}
	}
	return uids, nil
}

// fetch returns one part of one e-mail by UID, without marking it read.
func (c *imapConn) fetch(ctx context.Context, uid uint32, section string) ([]byte, error) {
	resps, err := c.cmd(ctx, "UID FETCH "+strconv.FormatUint(uint64(uid), 10)+" (BODY.PEEK["+section+"])")
	if err != nil {
		return nil, err
	}
	for _, r := range resps {
		if strings.Contains(r.Line, "FETCH") && len(r.Literals) > 0 {
			return bytes.Clone(r.Literals[0]), nil
		}
	}
	return nil, nil
}

// imapQuote writes a quoted IMAP string.
func imapQuote(s string) string {
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\r", "", "\n", "").Replace(s)
	return `"` + s + `"`
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
