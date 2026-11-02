package guardian

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// lang holds every word the pages say, so a deployment can pick its language.
type lang struct {
	code string // BCP 47, for <html lang>
	s    map[string]string
	// count writes "1 message" / "58 messages" with the language's plurals.
	count func(n int) string
	// when says when something happened the way a person would.
	when func(t, now time.Time) string
}

var languages = map[string]*lang{
	"en": {
		code: "en",
		s: map[string]string{
			"intro":        "Keeps a copy of the chats on our own server. On the phone it appears as a linked device named Guardian, which can be removed there at any time.",
			"connectors":   "Connectors",
			"chats":        "Chats",
			"chats_note":   "Only counts are shown in this phase; messages are not readable here.",
			"no_chats":     "No messages yet.",
			"group":        "group",
			"preview":      "Preview",
			"version":      "Version",
			"not_previews": "Not available in previews",
			"st_not":       "Not connected",
			"st_as":        "Connected as %s",
			"st_removed":   "Disconnected by the phone",
			"removed_next": "The device was removed on the phone. Connect again to continue.",
			"connect":      "Connect WhatsApp",
			"disconnect":   "Disconnect",
			"back":         "‹ Guardian",
			"instruction":  "Open WhatsApp on the phone, go to Linked devices, and scan this code.",
			"hint_1":       "The phone will show a linked device named ",
			"hint_2":       ". It can be removed there at any time.",
			"getting":      "Getting a code…",
			"unreachable":  "Could not reach WhatsApp. Check the server's internet connection and try again.",
			"expired":      "The code expired.",
			"no_code":      "Could not get a code.",
			"new_code_in":  "New code in %d s",
			"connected_go": "Connected. Taking you back…",
			"try_again":    "Try again",
			"qr_alt":       "QR code to scan in WhatsApp",
			"disc_title":   "Disconnect WhatsApp",
			"disc_q":       "Disconnect WhatsApp? Messages already kept stay here; new ones stop until you connect again.",
			"disc_keep":    "Keep connected",
			"nf_title":     "Not found",
			"nf_text":      "There is no such page here.",
			"title_sep":    " – Guardian",
		},
		count: func(n int) string {
			if n == 1 {
				return "1 message"
			}
			return thousands(n, " ") + " messages"
		},
		when: func(t, now time.Time) string {
			switch d := dayDiff(t, now); {
			case d == 0:
				return "today " + t.Format("15:04")
			case d == 1:
				return "yesterday " + t.Format("15:04")
			case d > 0 && d < 7:
				return t.Format("Mon 2 Jan")
			case t.Year() == now.Year():
				return t.Format("2 Jan")
			}
			return t.Format("2 Jan 2006")
		},
	},
	"cs": {
		code: "cs",
		s: map[string]string{
			"intro":        "Ukládá kopii chatů na náš vlastní server. V telefonu je vidět jako propojené zařízení Guardian a kdykoli ho tam jde odebrat.",
			"connectors":   "Konektory",
			"chats":        "Chaty",
			"chats_note":   "Zatím se ukazují jen počty, zprávy tady nejdou číst.",
			"no_chats":     "Zatím žádné zprávy.",
			"group":        "skupina",
			"preview":      "Náhled",
			"version":      "Verze",
			"not_previews": "V náhledu není k dispozici",
			"st_not":       "Nepřipojeno",
			"st_as":        "Připojeno jako %s",
			"st_removed":   "Odpojeno telefonem",
			"removed_next": "Zařízení bylo odebráno v telefonu. Pro pokračování ho znovu připoj.",
			"connect":      "Připojit WhatsApp",
			"disconnect":   "Odpojit",
			"back":         "‹ Guardian",
			"instruction":  "Otevři WhatsApp v telefonu, jdi do Propojená zařízení a naskenuj tento kód.",
			"hint_1":       "V telefonu se objeví propojené zařízení ",
			"hint_2":       ". Kdykoli ho tam jde odebrat.",
			"getting":      "Získávám kód…",
			"unreachable":  "Nepodařilo se spojit s WhatsAppem. Zkontroluj, že server má připojení k internetu, a zkus to znovu.",
			"expired":      "Kód vypršel.",
			"no_code":      "Nepodařilo se získat kód.",
			"new_code_in":  "Nový kód za %d s",
			"connected_go": "Připojeno. Vracím tě zpět…",
			"try_again":    "Zkusit znovu",
			"qr_alt":       "QR kód k naskenování ve WhatsAppu",
			"disc_title":   "Odpojit WhatsApp",
			"disc_q":       "Odpojit WhatsApp? Uložené zprávy zůstanou, nové se přestanou ukládat, dokud ho znovu nepřipojíš.",
			"disc_keep":    "Nechat připojené",
			"nf_title":     "Stránka nenalezena",
			"nf_text":      "Taková stránka tu není.",
			"title_sep":    " – Guardian",
		},
		count: func(n int) string {
			switch {
			case n == 1:
				return "1 zpráva"
			case n >= 2 && n <= 4:
				return strconv.Itoa(n) + " zprávy"
			}
			return thousands(n, " ") + " zpráv"
		},
		when: func(t, now time.Time) string {
			days := []string{"ne", "po", "út", "st", "čt", "pá", "so"}
			clock := strconv.Itoa(t.Hour()) + ":" + fmt.Sprintf("%02d", t.Minute())
			date := fmt.Sprintf("%d. %d.", t.Day(), int(t.Month()))
			switch d := dayDiff(t, now); {
			case d == 0:
				return "dnes " + clock
			case d == 1:
				return "včera " + clock
			case d > 0 && d < 7:
				return days[t.Weekday()] + " " + date
			case t.Year() == now.Year():
				return date
			}
			return fmt.Sprintf("%s %d", date, t.Year())
		},
	},
}

// pick returns the language for a code, English when unknown.
func pick(code string) *lang {
	if l, ok := languages[strings.ToLower(strings.TrimSpace(code))]; ok {
		return l
	}
	return languages["en"]
}

// T returns the text for a key, with any fmt arguments filled in.
func (l *lang) T(key string, args ...any) string {
	s, ok := l.s[key]
	if !ok {
		return key
	}
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}

// dayDiff is how many calendar days before now t is (negative if after).
func dayDiff(t, now time.Time) int {
	day := func(x time.Time) time.Time { return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, x.Location()) }
	return int(day(now).Sub(day(t)).Hours() / 24)
}

func thousands(n int, sep string) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + sep + s[i:]
	}
	return s
}
