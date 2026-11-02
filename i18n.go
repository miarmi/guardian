package guardian

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// lang holds every word the pages say, so a deployment can pick its language.
type lang struct {
	code string // BCP 47, for <html lang>
	name string // in English, for the model's prompt
	// contacts are the places a child can turn to beyond the family
	// (criterion 27): an intro line, then one line each.
	contacts []string
	// helpline is the first contact as a call button, e.g. Linka bezpečí.
	helplineLabel, helplineTel string
	// situations is the default set seeded on a fresh database.
	situations []Situation
	s          map[string]string
	// count writes "1 message" / "58 messages" with the language's plurals.
	count func(n int) string
	// when says when something happened the way a person would.
	when func(t, now time.Time) string
}

var languages = map[string]*lang{
	"en": {
		code: "en",
		s: map[string]string{
			"intro":                "Keeps a copy of the chats on our own server. On the phone it appears as a linked device named Guardian, which can be removed there at any time.",
			"connectors":           "Connectors",
			"chats":                "Chats",
			"chats_note":           "Only counts are shown in this phase; messages are not readable here.",
			"no_chats":             "No messages yet.",
			"group":                "group",
			"preview":              "Preview",
			"version":              "Version",
			"not_previews":         "Not available in previews",
			"st_not":               "Not connected",
			"st_as":                "Connected as %s",
			"st_removed":           "Disconnected by the phone",
			"removed_next":         "The device was removed on the phone. Connect again to continue.",
			"connect":              "Connect WhatsApp",
			"disconnect":           "Disconnect",
			"back":                 "‹ Guardian",
			"instruction":          "Open WhatsApp on the phone, go to Linked devices, and scan this code.",
			"hint_1":               "The phone will show a linked device named ",
			"hint_2":               ". It can be removed there at any time.",
			"getting":              "Getting a code…",
			"unreachable":          "Could not reach WhatsApp. Check the server's internet connection and try again.",
			"expired":              "The code expired.",
			"no_code":              "Could not get a code.",
			"new_code_in":          "New code in %d s",
			"connected_go":         "Connected. Taking you back…",
			"try_again":            "Try again",
			"qr_alt":               "QR code to scan in WhatsApp",
			"disc_title":           "Disconnect WhatsApp",
			"disc_q":               "Disconnect WhatsApp? Messages already kept stay here; new ones stop until you connect again.",
			"disc_keep":            "Keep connected",
			"nf_title":             "Not found",
			"nf_text":              "There is no such page here.",
			"title_sep":            " – Guardian",
			"nav_findings":         "Findings",
			"nav_situations":       "Situations",
			"nav_settings":         "Settings",
			"analysis":             "Analysis",
			"no_model":             "Analysis is not running: no AI model is connected.",
			"goes_to":              "The text of the messages is sent for analysis to: %s",
			"stays_home":           "The text of the messages stays on your server.",
			"an_failed":            "Analysis failed: %s",
			"an_limit":             "The daily limit of questions is used up; analysis continues tomorrow.",
			"an_running":           "Analysis runs every hour.",
			"an_preview":           "Analysis does not run in previews.",
			"alert_banner":         "New serious finding: %s in chat %s",
			"open_findings":        "%d open",
			"set_title":            "Settings",
			"set_intro":            "Connect any AI model through its API. It reads the chats and looks for the situations you set.",
			"f_type":               "Type",
			"f_address":            "Address",
			"f_model":              "Model",
			"f_key":                "Key",
			"f_limit":              "Daily question limit",
			"key_saved":            "Saved key ending %s. Leave empty to keep it.",
			"key_none":             "No key saved yet.",
			"t_anthropic":          "Anthropic",
			"t_openai":             "OpenAI-compatible",
			"t_practice":           "Practice",
			"t_openai_hint":        "OpenAI, Mistral, OpenRouter, Ollama, LM Studio… Address including /v1, e.g. https://api.openai.com/v1 or http://192.168.1.10:11434/v1 (Ollama).",
			"save":                 "Save",
			"test":                 "Test",
			"saved":                "Saved.",
			"model_ok":             "The model answers.",
			"model_bad":            "The model does not answer: %s",
			"model_incomplete":     "fill in the type, address, model and key first",
			"model_empty":          "the answer was empty",
			"sit_title":            "Situations",
			"sit_intro":            "What the agent watches for, and what it does when it finds it. The description goes to the model as written.",
			"sit_add":              "Add situation",
			"sit_new":              "New situation",
			"f_name":               "Name",
			"f_desc":               "Description",
			"f_sens":               "Sensitivity",
			"f_action":             "What to do",
			"f_enabled":            "On",
			"sens_low":             "low",
			"sens_medium":          "medium",
			"sens_high":            "high",
			"sens_help":            "High reports even faint signs; low only clear cases.",
			"act_record":           "Just record",
			"act_digest":           "Into the weekly digest",
			"act_alert":            "Alert at once",
			"sit_delete":           "Delete",
			"sit_builtin":          "built in, can only be switched off",
			"sit_off":              "off",
			"sit_incomplete":       "Fill in the name and the description.",
			"fin_title":            "Findings",
			"fin_intro":            "What the agent found. Written so the child can read it too.",
			"fin_none":             "No findings yet.",
			"fin_resolved":         "Resolved",
			"fin_false":            "False alarm",
			"fin_marked_resolved":  "Resolved",
			"fin_marked_false":     "False alarm",
			"fin_in_chat":          "in chat %s",
			"child":                "child",
			"practice_reason":      "Practice finding.",
			"practice_unreachable": "the practice model is unreachable",
			"tried":                "Tried: %s at %s",
			"key_forgotten":        "The saved key belongs to another address; enter a new one.",
			"nokey_warn":           "Without a key the analysis won't run.",
			"last_run":             "Last run %s.",
			"next_try":             "Next try at %s.",
			"open_arrow":           "Open ›",
			"settings_arrow":       "Settings ›",
			"testing":              "Testing…",
			"ph_openai":            "e.g. https://api.openai.com/v1",
			"ph_model":             "e.g. gpt-4o-mini or llama3",
			"limit_hint":           "One question = one chat in one hourly check.",
			"sit_saved":            "Saved.",
			"sit_builtin_long":     "Default situation: can be switched off, not deleted.",
			"sens_hint":            "High reports even faint signs; low only clear cases.",
			"del_title":            "Delete situation",
			"del_q":                "Delete situation “%s”? The description is lost; its findings stay.",
			"del_keep":             "Keep",
			"fin_reopen":           "Reopen",
			"chip_alert":           "Alert at once",
			"err_key":              "the key was refused, check it (%s)",
			"err_notfound":         "model or address not found (404)",
			"err_busy":             "the provider reports too many requests (429)",
			"err_server":           "the provider has a problem (%s)",
			"err_unreachable":      "the address doesn't answer",
			"err_unreadable":       "the model's answer could not be read",
			"err_timeout":          "the address doesn't respond (timed out)",
			"miss_key":             "The key is missing.",
			"miss_address":         "The address is missing.",
			"miss_model":           "The model is missing.",
			"no_key_reason":        "The model key is missing.",
			"dest_pending":         "Fill in the address to see where the message text goes.",
			"sit_added":            "Added.",
			"key_retype":           "The key from the test wasn't saved; enter it again before saving.",
			"push_title":           "Guardian: %s",
			"push_body":            "in chat %s",
			"help_banner":          "The child asks for help: %s in chat %s",
			"help_label":           "The child asks for help",
			"child_message_label":  "The child wrote:",
			"child_subject":        "Guardian found something",
			"child_intro":          "Guardian noticed something in one of your chats. You don't have to do anything. If you'd like your parents to help, just press the button.",
			"child_button":         "I want my parents to deal with it",
			"child_button_hint":    "Your parents know about this finding too. If you click, they'll see that you want them to deal with it, and anything you write.",
			"digest_subject":       "Guardian: week %s–%s",
			"digest_none":          "Guardian found nothing this week.",
			"digest_intro":         "What Guardian found this week:",
			"digest_chats":         "Chats looked at: %d",
			"digest_resolved":      "resolved",
			"digest_false":         "false alarm",
			"digest_open":          "open",
			"practice_mail_down":   "the practice mail is down",
			"mail_incomplete":      "fill in the tenant, app ID, secret and sender first",
			"mail_no_parents":      "fill in the parents' e-mail first",
			"test_mail_subject":    "Guardian: test e-mail",
			"test_mail_text":       "This is a test e-mail from Guardian. If you can read it, e-mail works.",
			"notif_title":          "Notifications",
			"notif_on":             "Turn on notifications on this device",
			"notif_devices":        "Devices with notifications",
			"notif_none":           "No device yet.",
			"notif_off":            "Switch off",
			"notif_unsupported":    "This browser can't receive notifications. On an iPhone, add Guardian to the home screen first and open it from there.",
			"notif_denied":         "Notifications are blocked for Guardian in this browser's settings.",
			"notif_added":          "Notifications are on for this device.",
			"mail_title":           "E-mail",
			"f_child_email":        "Child's e-mail",
			"f_parent_emails":      "Parents' e-mails",
			"f_parent_hint":        "Several addresses separated by commas.",
			"f_sending":            "Sending",
			"s_graph":              "Microsoft 365",
			"s_smtp":               "SMTP",
			"s_practice":           "Practice",
			"f_tenant":             "Tenant (directory ID)",
			"f_app_id":             "App ID (client ID)",
			"f_app_secret":         "App secret",
			"f_sender":             "Sender mailbox",
			"f_host":               "SMTP server",
			"f_port":               "Port",
			"f_user":               "User",
			"f_password":           "Password",
			"graph_hint":           "In Microsoft Entra, register an app with the application permission Mail.Send for Microsoft Graph, ideally limited to the sender mailbox only.",
			"mail_test":            "Send a test e-mail",
			"mail_ok":              "The e-mail went out.",
			"mail_bad":             "The e-mail didn't go out: %s",
			"mail_saved":           "Saved.",
			"secret_saved":         "Saved secret ending %s. Leave empty to keep it.",
			"f_who":                "Who is told",
			"who_parents":          "parents",
			"who_child":            "child",
			"who_both":             "both",
			"kid_title":            "Guardian",
			"kid_found":            "Guardian noticed: %s in chat %s.",
			"kid_confirm":          "Yes, let my parents deal with it",
			"kid_message":          "Do you want to add something?",
			"kid_thanks":           "Thanks. Your parents will see it now.",
			"kid_expired":          "This link is no longer valid.",
			"kid_already":          "Your request for help went out %s. Your parents can see it.",
			"hello":                "Hi,",
			"child_hint_secret":    "Your parents don't know about this yet. They'll see it once you click.",
			"kid_nothing_yet":      "Nothing has happened yet. When you press the button, your parents get a notification on their phone.",
			"kid_optional":         "Optional. You can write what's going on or how you feel.",
			"kid_resolved":         "Your parents marked this as resolved.",
			"help_wrote":           " · wrote a message",
			"push_test":            "Test notification.",
			"push_test_button":     "Send a test notification",
			"push_sent":            "The test notification is on its way.",
			"this_device":          "(this device)",
			"notif_on_here":        "Notifications are on for this device.",
			"notif_home_screen":    "On an iPhone, notifications only work from the home screen: Share → Add to Home Screen, then open Guardian from there.",
			"mail_child_failed":    "The e-mail to the child didn't go out: %s",
			"bad_address":          "\"%s\" is not a valid e-mail address.",
			"dev_browser":          "Browser",
		},
		name:          "English",
		helplineLabel: "", helplineTel: "",
		contacts: []string{
			"If it can't be solved at home, you can also turn elsewhere:",
			"A child helpline in your country: childhelplineinternational.org",
			"At school: your class teacher or the school counsellor",
			"If you're in danger right now: your emergency number (112 in Europe)",
		},
		situations: defaultSituationsEN,
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
			"intro":                "Ukládá kopii chatů na náš vlastní server. V telefonu je vidět jako propojené zařízení Guardian a kdykoli ho tam jde odebrat.",
			"connectors":           "Konektory",
			"chats":                "Chaty",
			"chats_note":           "Zatím se ukazují jen počty, zprávy tady nejdou číst.",
			"no_chats":             "Zatím žádné zprávy.",
			"group":                "skupina",
			"preview":              "Náhled",
			"version":              "Verze",
			"not_previews":         "V náhledu není k dispozici",
			"st_not":               "Nepřipojeno",
			"st_as":                "Připojeno jako %s",
			"st_removed":           "Odpojeno telefonem",
			"removed_next":         "Zařízení bylo odebráno v telefonu. Pro pokračování ho znovu připoj.",
			"connect":              "Připojit WhatsApp",
			"disconnect":           "Odpojit",
			"back":                 "‹ Guardian",
			"instruction":          "Otevři WhatsApp v telefonu, jdi do Propojená zařízení a naskenuj tento kód.",
			"hint_1":               "V telefonu se objeví propojené zařízení ",
			"hint_2":               ". Kdykoli ho tam jde odebrat.",
			"getting":              "Získávám kód…",
			"unreachable":          "Nepodařilo se spojit s WhatsAppem. Zkontroluj, že server má připojení k internetu, a zkus to znovu.",
			"expired":              "Kód vypršel.",
			"no_code":              "Nepodařilo se získat kód.",
			"new_code_in":          "Nový kód za %d s",
			"connected_go":         "Připojeno. Vracím tě zpět…",
			"try_again":            "Zkusit znovu",
			"qr_alt":               "QR kód k naskenování ve WhatsAppu",
			"disc_title":           "Odpojit WhatsApp",
			"disc_q":               "Odpojit WhatsApp? Uložené zprávy zůstanou, nové se přestanou ukládat, dokud ho znovu nepřipojíš.",
			"disc_keep":            "Nechat připojené",
			"nf_title":             "Stránka nenalezena",
			"nf_text":              "Taková stránka tu není.",
			"title_sep":            " – Guardian",
			"nav_findings":         "Nálezy",
			"nav_situations":       "Situace",
			"nav_settings":         "Nastavení",
			"analysis":             "Analýza",
			"no_model":             "Analýza neběží: není připojený žádný AI model.",
			"goes_to":              "Text zpráv se k vyhodnocení posílá do: %s",
			"stays_home":           "Text zpráv zůstává na tvém serveru.",
			"an_failed":            "Analýza se nepovedla: %s",
			"an_limit":             "Denní limit dotazů je vyčerpaný, analýza pokračuje zítra.",
			"an_running":           "Analýza běží každou hodinu.",
			"an_preview":           "V náhledu analýza neběží.",
			"alert_banner":         "Nový vážný nález: %s v chatu %s",
			"open_findings":        "otevřené: %d",
			"set_title":            "Nastavení",
			"set_intro":            "Připoj libovolný AI model přes jeho API. Bude číst chaty a hledat situace, které nastavíš.",
			"f_type":               "Typ",
			"f_address":            "Adresa",
			"f_model":              "Model",
			"f_key":                "Klíč",
			"f_limit":              "Denní limit dotazů",
			"key_saved":            "Uložený klíč končí na %s. Nech prázdné, pokud ho nechceš měnit.",
			"key_none":             "Zatím není uložený žádný klíč.",
			"t_anthropic":          "Anthropic",
			"t_openai":             "OpenAI-kompatibilní",
			"t_practice":           "Zkušební",
			"t_openai_hint":        "OpenAI, Mistral, OpenRouter, Ollama, LM Studio… Adresa včetně /v1, např. https://api.openai.com/v1 nebo http://192.168.1.10:11434/v1 (Ollama).",
			"save":                 "Uložit",
			"test":                 "Vyzkoušet",
			"saved":                "Uloženo.",
			"model_ok":             "Model odpovídá.",
			"model_bad":            "Model neodpovídá: %s",
			"model_incomplete":     "nejdřív vyplň typ, adresu, model a klíč",
			"model_empty":          "odpověď byla prázdná",
			"sit_title":            "Situace",
			"sit_intro":            "Co agent hlídá a co udělá, když to najde. Popis dostane model přesně tak, jak ho napíšeš.",
			"sit_add":              "Přidat situaci",
			"sit_new":              "Nová situace",
			"f_name":               "Název",
			"f_desc":               "Popis",
			"f_sens":               "Citlivost",
			"f_action":             "Co udělat",
			"f_enabled":            "Zapnuto",
			"sens_low":             "nízká",
			"sens_medium":          "střední",
			"sens_high":            "vysoká",
			"sens_help":            "Vysoká hlásí i slabé náznaky, nízká jen jasné případy.",
			"act_record":           "Jen zaznamenat",
			"act_digest":           "Do týdenního přehledu",
			"act_alert":            "Upozornit hned",
			"sit_delete":           "Smazat",
			"sit_builtin":          "výchozí, jde jen vypnout",
			"sit_off":              "vypnuto",
			"sit_incomplete":       "Vyplň název a popis.",
			"fin_title":            "Nálezy",
			"fin_intro":            "Co agent našel. Psané tak, aby to mohlo číst i dítě.",
			"fin_none":             "Zatím žádné nálezy.",
			"fin_resolved":         "Vyřešeno",
			"fin_false":            "Planý poplach",
			"fin_marked_resolved":  "Vyřešeno",
			"fin_marked_false":     "Planý poplach",
			"fin_in_chat":          "v chatu %s",
			"child":                "dítě",
			"practice_reason":      "Zkušební nález.",
			"practice_unreachable": "zkušební model je nedostupný",
			"tried":                "Vyzkoušeno: %s na %s",
			"key_forgotten":        "Klíč patří k jiné adrese, zadej nový.",
			"nokey_warn":           "Bez klíče analýza nepoběží.",
			"last_run":             "Naposledy %s.",
			"next_try":             "Další pokus v %s.",
			"open_arrow":           "Otevřít ›",
			"settings_arrow":       "Nastavení ›",
			"testing":              "Zkouším…",
			"ph_openai":            "např. https://api.openai.com/v1",
			"ph_model":             "např. gpt-4o-mini nebo llama3",
			"limit_hint":           "Jeden dotaz = jeden chat při jedné hodinové kontrole.",
			"sit_saved":            "Uloženo.",
			"sit_builtin_long":     "Výchozí situace, nejde smazat, jen vypnout.",
			"sens_hint":            "Vysoká hlásí i slabé náznaky, nízká jen jasné případy.",
			"del_title":            "Smazat situaci",
			"del_q":                "Smazat situaci „%s“? Popis se ztratí, nálezy z ní zůstanou.",
			"del_keep":             "Nechat",
			"fin_reopen":           "Znovu otevřít",
			"chip_alert":           "Upozornit hned",
			"err_key":              "klíč nebyl přijat, zkontroluj ho (%s)",
			"err_notfound":         "model nebo adresa neexistuje (404)",
			"err_busy":             "poskytovatel hlásí příliš mnoho dotazů (429)",
			"err_server":           "poskytovatel má potíže (%s)",
			"err_unreachable":      "adresa neodpovídá",
			"err_unreadable":       "odpověď modelu nešla přečíst",
			"err_timeout":          "adresa neodpovídá (vypršel čas)",
			"miss_key":             "Chybí klíč.",
			"miss_address":         "Chybí adresa.",
			"miss_model":           "Chybí model.",
			"no_key_reason":        "Chybí klíč k modelu.",
			"dest_pending":         "Kam se text zpráv posílá, uvidíš po vyplnění adresy.",
			"sit_added":            "Přidáno.",
			"key_retype":           "Klíč z posledního pokusu se neuložil, před uložením ho zadej znovu.",
			"push_title":           "Guardian: %s",
			"push_body":            "v chatu %s",
			"help_banner":          "Dítě žádá o pomoc: %s v chatu %s",
			"help_label":           "Dítě žádá o pomoc",
			"child_message_label":  "Dítě napsalo:",
			"child_subject":        "Guardian něco našel",
			"child_intro":          "Guardian si v jednom z tvých chatů něčeho všiml. Nemusíš nic dělat. Jestli chceš, aby ti s tím rodiče pomohli, stačí kliknout na tlačítko.",
			"child_button":         "Chci, aby to rodiče řešili",
			"child_button_hint":    "Rodiče o tom nálezu vědí taky. Když klikneš, uvidí, že chceš, aby to řešili, a co k tomu napíšeš.",
			"digest_subject":       "Guardian: týden %s–%s",
			"digest_none":          "Tento týden Guardian nic nenašel.",
			"digest_intro":         "Co Guardian tento týden našel:",
			"digest_chats":         "Prošlé chaty: %d",
			"digest_resolved":      "vyřešeno",
			"digest_false":         "planý poplach",
			"digest_open":          "otevřené",
			"practice_mail_down":   "zkušební pošta je nedostupná",
			"mail_incomplete":      "nejdřív vyplň tenant, ID aplikace, tajný klíč a odesílací schránku",
			"mail_no_parents":      "nejdřív vyplň e-mail rodičů",
			"test_mail_subject":    "Guardian: zkušební e-mail",
			"test_mail_text":       "Tohle je zkušební e-mail od Guardiana. Když ho čteš, pošta funguje.",
			"notif_title":          "Upozornění",
			"notif_on":             "Zapnout upozornění na tomto zařízení",
			"notif_devices":        "Zařízení s upozorněním",
			"notif_none":           "Zatím žádné zařízení.",
			"notif_off":            "Vypnout",
			"notif_unsupported":    "Tenhle prohlížeč upozornění neumí. Na iPhonu nejdřív přidej Guardiana na plochu a otevři ho odtamtud.",
			"notif_denied":         "Upozornění jsou pro Guardiana v nastavení prohlížeče zakázaná.",
			"notif_added":          "Upozornění jsou na tomto zařízení zapnutá.",
			"mail_title":           "E-mail",
			"f_child_email":        "E-mail dítěte",
			"f_parent_emails":      "E-maily rodičů",
			"f_parent_hint":        "Víc adres odděl čárkou.",
			"f_sending":            "Odesílání",
			"s_graph":              "Microsoft 365",
			"s_smtp":               "SMTP",
			"s_practice":           "Zkušební",
			"f_tenant":             "Tenant (ID adresáře)",
			"f_app_id":             "ID aplikace (client ID)",
			"f_app_secret":         "Tajný klíč aplikace",
			"f_sender":             "Odesílací schránka",
			"f_host":               "SMTP server",
			"f_port":               "Port",
			"f_user":               "Uživatel",
			"f_password":           "Heslo",
			"graph_hint":           "V Microsoft Entra zaregistruj aplikaci s oprávněním aplikace Mail.Send pro Microsoft Graph, nejlépe omezeným jen na odesílací schránku.",
			"mail_test":            "Poslat zkušební e-mail",
			"mail_ok":              "E-mail odešel.",
			"mail_bad":             "E-mail neodešel: %s",
			"mail_saved":           "Uloženo.",
			"secret_saved":         "Uložený klíč končí na %s. Nech prázdné, pokud ho nechceš měnit.",
			"f_who":                "Komu",
			"who_parents":          "rodičům",
			"who_child":            "dítěti",
			"who_both":             "oběma",
			"kid_title":            "Guardian",
			"kid_found":            "Guardian si všiml: %s v chatu %s.",
			"kid_confirm":          "Ano, ať to řeší rodiče",
			"kid_message":          "Chceš k tomu něco napsat?",
			"kid_thanks":           "Díky. Rodiče to teď uvidí.",
			"kid_expired":          "Tenhle odkaz už neplatí.",
			"kid_already":          "Žádost o pomoc už odešla %s. Rodiče ji vidí.",
			"hello":                "Ahoj,",
			"child_hint_secret":    "Rodiče o tomhle zatím nevědí. Uvidí to, až klikneš.",
			"kid_nothing_yet":      "Zatím se nic nestalo. Až zmáčkneš tlačítko, rodiče dostanou upozornění na telefon.",
			"kid_optional":         "Nepovinné. Můžeš napsat, co se děje nebo jak ti je.",
			"kid_resolved":         "Rodiče to označili jako vyřešené.",
			"help_wrote":           " · napsalo zprávu",
			"push_test":            "Zkušební upozornění.",
			"push_test_button":     "Poslat zkušební upozornění",
			"push_sent":            "Zkušební upozornění je na cestě.",
			"this_device":          "(toto zařízení)",
			"notif_on_here":        "Upozornění jsou na tomto zařízení zapnutá.",
			"notif_home_screen":    "Na iPhonu fungují upozornění jen z plochy: Sdílet → Přidat na plochu, pak otevři Guardiana z plochy.",
			"mail_child_failed":    "E-mail dítěti neodešel: %s",
			"bad_address":          "„%s“ není platná e-mailová adresa.",
			"dev_browser":          "Prohlížeč",
		},
		name:          "Czech",
		helplineLabel: "Zavolat na Linku bezpečí 116 111", helplineTel: "116111",
		contacts: []string{
			"Když to nejde vyřešit doma, můžeš se obrátit i jinam:",
			"Linka bezpečí: 116 111 – zdarma, nonstop, i chat a e-mail na linkabezpeci.cz",
			"E-Bezpečí – poradna pro kyberšikanu, vydírání a falešné profily: e-bezpeci.cz",
			"Linka první psychické pomoci: 116 123",
			"Ve škole: třídní učitel nebo školní metodik prevence",
			"Když jde o bezpečí hned: 158 (policie) nebo 112",
		},
		situations: defaultSituationsCS,
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

var defaultSituationsCS = []Situation{
	{Name: "Urážky a ponižování", Sensitivity: "medium", Action: "digest",
		Description: "Opakované nadávky, zesměšňování nebo ponižování dítěte, zvlášť od stejných lidí nebo před ostatními ve skupině."},
	{Name: "Vylučování ze skupiny", Sensitivity: "medium", Action: "digest",
		Description: "Dítě je záměrně vynecháváno: nezvou ho, jeho zprávy se přehlížejí, ostatní se domlouvají bez něj nebo ho vyhazují ze skupin."},
	{Name: "Výhrůžky", Sensitivity: "medium", Action: "alert",
		Description: "Kdokoli dítěti vyhrožuje ublížením, zbitím, zveřejněním něčeho o něm nebo jinou újmou."},
	{Name: "Vydírání nebo nátlak na fotky", Sensitivity: "medium", Action: "alert",
		Description: "Někdo po dítěti chce fotky nebo videa, zvlášť intimní, nebo ho k něčemu tlačí pod pohrůžkou či výměnou za něco."},
	{Name: "Kontakt od neznámého dospělého", Sensitivity: "medium", Action: "alert",
		Description: "Píše dítěti někdo, kdo se zdá být dospělý a koho dítě nezná ze skutečného života; zvlášť když lichotí, chce tajemství, schůzku nebo přechod do jiné aplikace."},
	{Name: "Dítě je v nouzi", Sensitivity: "medium", Action: "alert",
		Description: "Dítě píše, že je mu zle, že nechce do školy, že je na všechno samo, nebo naznačuje, že by si mohlo ublížit."},
}

var defaultSituationsEN = []Situation{
	{Name: "Insults and humiliation", Sensitivity: "medium", Action: "digest",
		Description: "Repeated name-calling, mocking or humiliating the child, especially by the same people or in front of others in a group."},
	{Name: "Exclusion from the group", Sensitivity: "medium", Action: "digest",
		Description: "The child is left out on purpose: not invited, their messages ignored, others making plans without them or removing them from groups."},
	{Name: "Threats", Sensitivity: "medium", Action: "alert",
		Description: "Anyone threatens the child with harm, a beating, publishing something about them or any other damage."},
	{Name: "Blackmail or pressure for photos", Sensitivity: "medium", Action: "alert",
		Description: "Someone asks the child for photos or videos, especially intimate ones, or pushes them into something under threat or in exchange for something."},
	{Name: "Contact from an unknown adult", Sensitivity: "medium", Action: "alert",
		Description: "Someone who seems to be an adult, and whom the child does not know in real life, writes to the child; especially with flattery, asking for secrets, a meeting or a move to another app."},
	{Name: "The child is in distress", Sensitivity: "medium", Action: "alert",
		Description: "The child writes that they feel bad, do not want to go to school, are alone with everything, or hints that they might hurt themselves."},
}

var httpCode = regexp.MustCompile(`HTTP (\d{3})`)

// explain turns a model error into words for people, keeping the code.
func (l *lang) explain(err error) string {
	msg := err.Error()
	low := strings.ToLower(msg)
	if m := httpCode.FindStringSubmatch(msg); m != nil {
		switch c := m[1]; {
		case c == "401" || c == "403":
			return l.T("err_key", c)
		case c == "404":
			return l.T("err_notfound")
		case c == "429":
			return l.T("err_busy")
		case c[0] == '5':
			return l.T("err_server", c)
		}
	}
	for _, s := range []string{"timeout", "deadline exceeded"} {
		if strings.Contains(low, s) {
			return l.T("err_timeout")
		}
	}
	for _, s := range []string{"no such host", "connection refused", "network is unreachable", "connection reset", "eof"} {
		if strings.Contains(low, s) {
			return l.T("err_unreachable")
		}
	}
	for _, s := range []string{"could not be read", "was not json", "answer was empty"} {
		if strings.Contains(low, s) {
			return l.T("err_unreadable")
		}
	}
	return msg
}

// day writes a short date: "26. 10." in Czech, "26 Oct" in English.
func (l *lang) day(t time.Time) string {
	if l.code == "cs" {
		return fmt.Sprintf("%d. %d.", t.Day(), int(t.Month()))
	}
	return t.Format("2 Jan")
}
