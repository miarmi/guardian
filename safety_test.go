package guardian

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

func outboxText(a *App) string {
	a.nt.mu.Lock()
	defer a.nt.mu.Unlock()
	var b strings.Builder
	for _, o := range a.nt.outbox {
		b.WriteString(o.Kind + "|" + o.Subject + "|" + o.Text + "\n")
	}
	return b.String()
}

func TestStrangerWritesOnce(t *testing.T) {
	a, do := practiceApp(t)
	msg := url.Values{"chat": {"s1"}, "sender_name": {"Cizí Pán"}, "text": {"ahoj"}, "known": {"0"}}
	do("POST", "/dev/whatsapp/message", msg)
	do("POST", "/dev/whatsapp/message", msg)
	do("POST", "/dev/whatsapp/message", url.Values{"chat": {"s2"}, "sender_name": {"Kamarád"}, "text": {"ahoj"}})
	do("POST", "/dev/whatsapp/message", url.Values{"chat": {"g1"}, "group": {"1"}, "sender_name": {"Jiný"}, "text": {"x"}, "known": {"0"}})
	out := outboxText(a)
	if strings.Count(out, "Guardian: neznámý kontakt|Cizí Pán píše dítěti") != 1 || strings.Contains(out, "Kamarád") || strings.Contains(out, "Jiný") || strings.Contains(out, "ahoj") {
		t.Fatalf("pushes: %s", out)
	}
	if !strings.Contains(do("GET", "/", nil).Body.String(), "Neznámý kontakt: Cizí Pán") {
		t.Fatal("no banner")
	}
	page := do("GET", "/findings", nil).Body.String()
	if !strings.Contains(page, "Neznámé kontakty") || !strings.Contains(page, "V pořádku") {
		t.Fatal("no part in Nálezy")
	}
	if strings.Contains(do("GET", "/", nil).Body.String(), "Neznámý kontakt:") {
		t.Fatal("banner stays after Nálezy")
	}
	ss, _ := a.store.Strangers(context.Background())
	do("POST", "/strangers/"+itoa(ss[0].ID)+"/ok", nil)
	if ss, _ := a.store.Strangers(context.Background()); !ss[0].OK {
		t.Fatal("not marked")
	}
}

func TestAddedToGroupByStranger(t *testing.T) {
	a, do := practiceApp(t)
	do("POST", "/dev/whatsapp/added", url.Values{"chat": {"g9"}, "chat_name": {"Párty"}, "by": {"Neznámý"}, "known": {"0"}})
	do("POST", "/dev/whatsapp/added", url.Values{"chat": {"g8"}, "chat_name": {"Třída"}, "by": {"Učitel"}, "known": {"1"}})
	out := outboxText(a)
	if !strings.Contains(out, "Neznámý přidal(a) dítě do skupiny Párty") || strings.Contains(out, "Učitel") {
		t.Fatalf("pushes: %s", out)
	}
}

func TestNudePhoto(t *testing.T) {
	a, do := practiceApp(t)
	do("POST", "/dev/whatsapp/message", url.Values{"chat": {"p1"}, "chat_name": {"Někdo"}, "sender_name": {"Někdo"}, "photo": {"plain"}})
	do("POST", "/dev/whatsapp/message", url.Values{"chat": {"p2"}, "chat_name": {"Cizí"}, "sender_name": {"Cizí"}, "photo": {"nude"}, "known": {"0"}})
	fs, _ := a.store.Findings(context.Background())
	if len(fs) != 1 || fs[0].Reason != "Dítě dostalo fotku, která vypadá jako nahota, od Cizí. Není v kontaktech." || len(fs[0].Quotes) != 0 {
		t.Fatalf("findings: %+v", fs)
	}
	if !strings.Contains(outboxText(a), "Guardian: Možná nahá fotka") {
		t.Fatal("no push")
	}
	sits, _ := a.store.Situations(context.Background())
	if on, _ := a.an.enabledSituations(context.Background()); len(on) != len(sits)-1 {
		t.Fatal("the photo situation went to the model")
	}
}

func TestPrivacyFixAndChange(t *testing.T) {
	a, do := practiceApp(t)
	page := do("GET", "/", nil).Body.String()
	if !strings.Contains(page, "Nastavení soukromí") || strings.Count(page, "Riskantní") != 6 {
		t.Fatal("rows not shown as risky")
	}
	do("POST", "/whatsapp/privacy", nil)
	if p, _ := a.wa.Privacy(context.Background()); len(p.risky()) != 0 {
		t.Fatal("not set")
	}
	if !strings.Contains(do("GET", "/?privacy=done", nil).Body.String(), "Vše je nastavené bezpečně.") {
		t.Fatal("not all safe")
	}
	do("POST", "/dev/whatsapp/privacy", url.Values{"groupadd": {"all"}})
	if !strings.Contains(outboxText(a), "Guardian: nastavení WhatsAppu se změnilo|Přidávání do skupin") {
		t.Fatalf("no push: %s", outboxText(a))
	}
}

func TestPhoneReminder(t *testing.T) {
	_, do := practiceApp(t)
	if !strings.Contains(do("GET", "/", nil).Body.String(), "Zkontroluj nastavení v telefonu dítěte.") {
		t.Fatal("no reminder")
	}
	do("POST", "/platforms/checked", nil)
	if strings.Contains(do("GET", "/", nil).Body.String(), "Zkontroluj nastavení") {
		t.Fatal("reminder stays")
	}
	if !strings.Contains(do("GET", "/platforms", nil).Body.String(), "Naposledy zkontrolováno") {
		t.Fatal("no date")
	}
}
