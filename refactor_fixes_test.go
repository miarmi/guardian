package guardian

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReleaseDigestMailsInOneStep(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	if err := st.SeedSituations(ctx, defaultSituationsEN); err != nil {
		t.Fatal(err)
	}
	sits, _ := st.Situations(ctx)
	now := time.Now()
	var ids []int64
	for _, chat := range []string{"a", "b", "c"} {
		id, _, err := st.RecordFinding(ctx, chat, sits[0].ID, "r", nil, now.Add(-time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	st.QueueChildMail(ctx, ids[0], time.Time{}) // waits for the digest
	st.QueueChildMail(ctx, ids[2], time.Time{}) // waits for the digest
	st.QueueChildMail(ctx, ids[1], now)         // an alert: already due
	got, err := st.ReleaseDigestMails(ctx, now)
	if err != nil || !slices.Equal(got, []int64{ids[0], ids[2]}) {
		t.Fatalf("released %v, %v; want %v", got, err, []int64{ids[0], ids[2]})
	}
	if again, _ := st.ReleaseDigestMails(ctx, now); len(again) != 0 {
		t.Fatalf("released twice: %v", again)
	}
	due, _ := st.ChildMailsDue(ctx, now.Add(time.Second))
	if !slices.Equal(due, ids) {
		t.Fatalf("due %v, want %v", due, ids)
	}
}

func TestAskForHelpCountsOnce(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	st.SeedSituations(ctx, defaultSituationsEN)
	sits, _ := st.Situations(ctx)
	id, _, _ := st.RecordFinding(ctx, "c", sits[0].ID, "r", nil, time.Now())
	if ok, err := st.AskForHelp(ctx, id, "first"); !ok || err != nil {
		t.Fatalf("first ask: %v %v", ok, err)
	}
	if ok, _ := st.AskForHelp(ctx, id, "second"); ok {
		t.Fatal("a second press counted again")
	}
	f, _, _ := st.Finding(ctx, id)
	if f.ChildMessage != "first" {
		t.Fatalf("message overwritten: %q", f.ChildMessage)
	}
}

func TestConcurrentHelpRequestsMergeWithoutLoss(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	const n = 16
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	news := 0
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, isNew, err := st.AddHelpRequest(ctx, "msg"+string(rune('a'+i)), func(time.Time) string { return "t" })
			if err != nil {
				t.Error(err)
			}
			if isNew {
				mu.Lock()
				news++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	reqs, _ := st.HelpRequests(ctx)
	if news != 1 || len(reqs) != 1 {
		t.Fatalf("%d new, %d requests; want one", news, len(reqs))
	}
	for i := range n {
		if m := "msg" + string(rune('a'+i)); !strings.Contains(reqs[0].Message, m) {
			t.Errorf("lost %q from %q", m, reqs[0].Message)
		}
	}
}

func TestMergeHelpMessage(t *testing.T) {
	stamp := func(t time.Time) string { return t.Format("15:04") }
	at := func(h, m int) time.Time { return time.Date(2026, 11, 2, h, m, 0, 0, time.UTC) }
	got := mergeHelpMessage("one", false, at(9, 0), "two", at(9, 5), stamp)
	if got != "09:00 one\n09:05 two" {
		t.Fatalf("got %q", got)
	}
	if got := mergeHelpMessage(got, true, at(9, 0), "three", at(9, 7), stamp); got != "09:00 one\n09:05 two\n09:07 three" {
		t.Fatalf("got %q", got)
	}
	if got := mergeHelpMessage("", false, at(9, 0), "late", at(9, 1), stamp); got != "09:01 late" {
		t.Fatalf("got %q", got)
	}
}

func TestSigningKeyIsMadeOnceUnderConcurrentFirstUse(t *testing.T) {
	n := &notifier{secretsDir: t.TempDir()}
	tokens := make([]string, 8)
	var wg sync.WaitGroup
	for i := range tokens {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tokens[i], _ = n.sign(tokenFinding, int64(i), time.Now().Add(time.Hour))
		}()
	}
	wg.Wait()
	fresh := &notifier{secretsDir: n.secretsDir} // reads the key back from disk
	for i, tok := range tokens {
		if id, ok := fresh.checkFindingToken(tok); !ok || id != int64(i) {
			t.Fatalf("token %d refused after a concurrent first use", i)
		}
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGraphTokenIsNotReusedForOtherCredentials(t *testing.T) {
	asked := 0
	old := mailHTTP
	mailHTTP = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		asked++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"new","expires_in":3600}`)), Header: http.Header{}}, nil
	})}
	defer func() { mailHTTP = old }()
	n := &notifier{}
	a := mailSettings{Tenant: "t", AppID: "id", AppSecret: "old-secret"}
	b := a
	b.AppSecret = "new-secret"
	// A token for the old secret, as if fetched while the settings changed.
	n.graph = graphToken{token: "stale", expires: time.Now().Add(time.Hour), creds: graphCreds(a)}
	if tok, _ := n.graphAccessToken(context.Background(), a); tok != "stale" || asked != 0 {
		t.Fatalf("cached token not used for the same credentials: %q, %d asks", tok, asked)
	}
	if tok, err := n.graphAccessToken(context.Background(), b); err != nil || tok != "new" || asked != 1 {
		t.Fatalf("got %q %v after %d asks; want a fresh token", tok, err, asked)
	}
}

func TestLabelSameNames(t *testing.T) {
	chats := []ChatSummary{
		{JID: "420777111222@s.whatsapp.net", Name: "Kuba", First: 5},
		{JID: "g2@g.us", Name: "Kuba", First: 9, IsGroup: true},
		{JID: "g1@g.us", Name: "Kuba", First: 2, IsGroup: true},
		{JID: "x@g.us", Name: "Eva", First: 1, IsGroup: true},
		{JID: "y@g.us", Name: "", First: 3},
		{JID: "z@g.us", Name: "", First: 4},
	}
	labelSameNames(chats)
	var got []string
	for _, c := range chats {
		got = append(got, c.Suffix)
	}
	if want := []string{"…222", "(3)", "(1)", "", "", ""}; !slices.Equal(got, want) {
		t.Fatalf("suffixes %q, want %q", got, want)
	}
}

func TestSettingsReadsSeveralKeys(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	st.SetSetting(ctx, "a", "1")
	st.SetSetting(ctx, "b", "2")
	v := st.Settings(ctx, "a", "b", "missing")
	if v["a"] != "1" || v["b"] != "2" || v["missing"] != "" || len(v) != 2 {
		t.Fatalf("got %v", v)
	}
}

func TestChatInfo(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	st.EnsureChat(ctx, "g@g.us", "6.B", true, true)
	if c, err := st.ChatInfo(ctx, "g@g.us"); err != nil || c.Name != "6.B" || !c.IsGroup {
		t.Fatalf("got %+v %v", c, err)
	}
	if c, err := st.ChatInfo(ctx, "unknown"); err != nil || c != (chatInfo{}) {
		t.Fatalf("unknown chat: %+v %v", c, err)
	}
}
