package guardian

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestPlatformOf(t *testing.T) {
	for addr, want := range map[string]string{
		"noreply@discord.com":       "Discord",
		"family@email.discord.com":  "Discord",
		"no-reply@roblox.com":       "Roblox",
		"help@acct.epicgames.com":   "Epic Games",
		"x@discord.com.example.net": "",
		"x@notroblox.com":           "",
		"kamarad@example.cz":        "",
		"broken":                    "",
	} {
		if got := platformOf(addr); got != want {
			t.Errorf("platformOf(%q) = %q, want %q", addr, got, want)
		}
	}
}

const sampleMail = "From: Discord <noreply@discord.com>\r\n" +
	"Subject: =?UTF-8?Q?T=C3=BDdenn=C3=AD_souhrn?=\r\n" +
	"Date: Sun, 01 Nov 2026 19:00:00 +0100\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/alternative; boundary=\"b\"\r\n\r\n" +
	"--b\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>ignored when plain exists</p>\r\n" +
	"--b\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n" +
	"Nov=C3=BD p=C5=99=C3=ADtel: Marek\r\n--b--\r\n"

func TestParseMail(t *testing.T) {
	m, err := parseMail([]byte(sampleMail))
	if err != nil {
		t.Fatal(err)
	}
	if m.Platform != "Discord" || m.Subject != "Týdenní souhrn" || !strings.Contains(m.Text, "Nový přítel: Marek") || m.Date.IsZero() {
		t.Fatalf("got %+v", m)
	}
	h, _ := parseMail([]byte("From: a@roblox.com\r\nContent-Type: text/html\r\n\r\n<style>x{}</style><p>Ahoj&nbsp;<b>rodiči</b></p>"))
	if strings.Contains(h.Text, "x{}") || !strings.Contains(h.Text, "rodiči") {
		t.Fatalf("html: %q", h.Text)
	}
}

// fakeIMAP is a scripted IMAP server: UID 5 is from Discord, UID 7 only
// looks like it. It records every command it gets.
type fakeIMAP struct {
	mu       sync.Mutex
	commands []string
}

func (f *fakeIMAP) serve(t *testing.T, ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			r := bufio.NewReader(conn)
			fmt.Fprint(conn, "* OK fake ready\r\n")
			for {
				line, err := r.ReadString('\n')
				if err != nil {
					return
				}
				line = strings.TrimRight(line, "\r\n")
				tag, cmd, _ := strings.Cut(line, " ")
				f.mu.Lock()
				f.commands = append(f.commands, cmd)
				f.mu.Unlock()
				switch {
				case strings.HasPrefix(cmd, "LOGIN"):
					if !strings.Contains(cmd, `"secret"`) {
						fmt.Fprintf(conn, "%s NO [AUTHENTICATIONFAILED] Invalid credentials\r\n", tag)
						continue
					}
				case strings.HasPrefix(cmd, "EXAMINE"):
					fmt.Fprint(conn, "* OK [UIDVALIDITY 42] UIDs valid\r\n")
				case strings.HasPrefix(cmd, "UID SEARCH"):
					fmt.Fprint(conn, "* SEARCH 5 7\r\n")
				case strings.HasPrefix(cmd, "UID FETCH 5 (BODY.PEEK[HEADER.FIELDS (FROM)])"):
					h := "From: Discord <noreply@discord.com>\r\n\r\n"
					fmt.Fprintf(conn, "* 1 FETCH (UID 5 BODY[HEADER.FIELDS (FROM)] {%d}\r\n%s)\r\n", len(h), h)
				case strings.HasPrefix(cmd, "UID FETCH 7 (BODY.PEEK[HEADER.FIELDS (FROM)])"):
					h := "From: Spam <x@discord.com.example.net>\r\n\r\n"
					fmt.Fprintf(conn, "* 2 FETCH (UID 7 BODY[HEADER.FIELDS (FROM)] {%d}\r\n%s)\r\n", len(h), h)
				case strings.HasPrefix(cmd, "UID FETCH 5 (BODY.PEEK[])"):
					fmt.Fprintf(conn, "* 1 FETCH (UID 5 BODY[] {%d}\r\n%s)\r\n", len(sampleMail), sampleMail)
				case strings.HasPrefix(cmd, "LOGOUT"):
					fmt.Fprintf(conn, "* BYE\r\n%s OK bye\r\n", tag)
					return
				default:
					fmt.Fprintf(conn, "%s BAD unexpected\r\n", tag)
					continue
				}
				fmt.Fprintf(conn, "%s OK done\r\n", tag)
			}
		}()
	}
}

func fakeGmail(t *testing.T) (gmailBox, *fakeIMAP) {
	srv := httptest.NewUnstartedServer(nil) // only for its test certificate
	srv.StartTLS()
	cert := srv.TLS.Certificates[0]
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	srv.Close()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	f := &fakeIMAP{}
	go f.serve(t, ln)
	return gmailBox{addr: ln.Addr().String(), tlsConfig: &tls.Config{RootCAs: pool, ServerName: "example.com"}}, f
}

func TestGmailReadsOnlyParentalMailAndChangesNothing(t *testing.T) {
	box, f := fakeGmail(t)
	g := gmailSettings{Address: "rodic@gmail.com", Password: "secret", Read: true}
	mails, validity, err := box.New(context.Background(), g, 0)
	if err != nil {
		t.Fatal(err)
	}
	if validity != 42 || len(mails) != 2 || mails[0].Platform != "Discord" || mails[0].Subject != "Týdenní souhrn" || mails[1].Platform != "" {
		t.Fatalf("got %+v (validity %d)", mails, validity)
	}
	f.mu.Lock()
	commands := append([]string(nil), f.commands...)
	f.mu.Unlock()
	all := strings.Join(commands, "\n")
	if strings.Contains(all, "UID FETCH 7 (BODY.PEEK[])") {
		t.Fatal("opened an e-mail from another sender")
	}
	for _, c := range commands {
		for _, bad := range []string{"SELECT", "STORE", "COPY", "MOVE", "EXPUNGE", "BODY[]", "BODY[HEADER"} {
			if strings.HasPrefix(c, bad) || (strings.Contains(c, bad) && !strings.Contains(c, "PEEK")) {
				t.Fatalf("a command that could change the mailbox: %q", c)
			}
		}
	}
	if !strings.Contains(all, `FROM "discord.com"`) || !strings.Contains(all, `FROM "epicgames.com"`) {
		t.Fatalf("search not limited to the senders:\n%s", all)
	}
	if err := box.Check(context.Background(), gmailSettings{Address: "rodic@gmail.com", Password: "wrong"}); err == nil || !strings.Contains(err.Error(), "Invalid credentials") {
		t.Fatalf("bad password: %v", err)
	}
}

func TestPracticeGmailSignalsFindingsAndSending(t *testing.T) {
	a, do := practiceApp(t)
	do("POST", "/settings", url.Values{"type": {"practice"}})
	do("POST", "/settings/gmail", url.Values{"gmail_type": {"practice"}, "gmail_read": {"1"}})
	do("POST", "/settings/mail", url.Values{"mail_type": {"gmail"}, "parent_emails": {"mama@example.cz"}})
	do("POST", "/dev/gmail/message", url.Values{"from": {"noreply@discord.com"}, "subject": {"Týdenní souhrn"}, "text": {"Nový přítel [Kontakt od cizího dospělého]"}})
	do("POST", "/dev/gmail/message", url.Values{"from": {"kamarad@example.cz"}, "subject": {"Soukromé"}, "text": {"[Výhrůžky násilím]"}})
	do("POST", "/dev/analysis/run", nil)
	do("POST", "/dev/analysis/run", nil) // read once only
	fin := do("GET", "/findings", nil).Body.String()
	if !strings.Contains(fin, "Z rodičovských nástrojů") || !strings.Contains(fin, "Týdenní souhrn") || strings.Contains(fin, "Soukromé") {
		t.Fatal("signal missing, doubled, or another sender read")
	}
	if !strings.Contains(fin, "Discord (rodičovský e-mail)") || strings.Contains(fin, "Výhrůžky násilím") {
		t.Fatal("finding from the signal missing, or one from another sender")
	}
	if sigs, _ := a.store.Signals(context.Background(), 10); len(sigs) != 1 || sigs[0].Summary != "Týdenní souhrn" {
		t.Fatalf("signals: %+v", sigs)
	}
	if !strings.Contains(fin, "E-mail Discord") {
		t.Fatal("the e-mail finding shows no evidence")
	}
	// Sending through Gmail (practice): the test e-mail goes to the outbox.
	if !strings.Contains(do("POST", "/settings/mail/test", url.Values{}).Body.String(), "E-mail odešel.") {
		t.Fatal("test e-mail through Gmail")
	}
	// A failing mailbox is said on the overview, and cleared when it works.
	do("POST", "/dev/gmail/unreachable", url.Values{"on": {"1"}})
	do("POST", "/dev/analysis/run", nil)
	if !strings.Contains(do("GET", "/", nil).Body.String(), "Gmail nejde přečíst: zkušební schránka je nedostupná") {
		t.Fatal("failure not shown")
	}
	do("POST", "/dev/gmail/unreachable", url.Values{"on": {"0"}})
	do("POST", "/dev/analysis/run", nil)
	if strings.Contains(do("GET", "/", nil).Body.String(), "Gmail nejde přečíst") {
		t.Fatal("failure stays")
	}
	// Platformy says the parental e-mails are connected.
	if p := do("GET", "/platforms", nil).Body.String(); !strings.Contains(p, "Přes rodičovské e-maily") || !strings.Contains(p, "Připojeno") {
		t.Fatal("platforms page")
	}
}
