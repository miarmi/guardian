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
			"model_empty":          "the answer was empty",
			"sit_title":            "Situations",
			"sit_intro":            "What Guardian watches for, and what it does when it finds it. The description goes to the model as written.",
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
			"sit_off":              "off",
			"sit_incomplete":       "Fill in the name and the description.",
			"fin_title":            "Findings",
			"fin_intro":            "What Guardian found. Written so the child can read it too.",
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
			"child_intro":          "Guardian noticed something in one of your chats. You don't have to do anything.",
			"child_button":         "I need help",
			"child_button_hint":    "If you want help with it, press the button: your parents get a notification that you need help, and they'll see what you write.",
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
			"f_parent_hint":        "One address per line, or separate them with commas.",
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
			"kid_title":            "Guardian",
			"kid_found":            "Guardian noticed: %s in chat %s.",
			"kid_confirm":          "Yes, I need help",
			"kid_message":          "Do you want to add something?",
			"kid_thanks":           "Thanks. Your parents now know you need help.",
			"kid_expired":          "This link is no longer valid.",
			"kid_already":          "Your request for help went out %s. Your parents know about it.",
			"hello":                "Hi,",
			"kid_nothing_yet":      "Nothing has happened yet. When you press the button, your parents get a notification on their phone.",
			"kid_optional":         "Optional. You can write what's going on or how you feel.",
			"kid_resolved":         "Your parents marked this as resolved.",
			"push_test":            "Test notification.",
			"push_test_button":     "Send a test notification",
			"push_sent":            "The test notification is on its way.",
			"this_device":          "this device",
			"notif_on_here":        "Notifications are on for this device.",
			"notif_home_screen":    "On an iPhone, notifications only work from the home screen: Share → Add to Home Screen, then open Guardian from there.",
			"mail_child_failed":    "The e-mail to the child didn't go out: %s",
			"bad_address":          "\"%s\" is not a valid e-mail address.",
			"child_parents_know":   "Your parents already know.",
			"child_other_intro":    "If something else is going on:",
			"child_other_link":     "I need help with something else",
			"help_request":         "The child asks for help",
			"hl_intro":             "Here you can let your parents know at any time that you need help. With anything, also with something Guardian didn't find.",
			"hl_field":             "What's going on?",
			"hl_button":            "I need help",
			"helplink_subject":     "Guardian: when you need help",
			"helplink_mail_intro":  "This is your own link to let your parents know at any time that you need help. With anything, also with something Guardian didn't find. Your parents get a notification on their phone.",
			"helplink_valid":       "The link is valid until %s.",
			"helplink_title":       "Help link for the child",
			"helplink_send":        "Send the child a help link",
			"helplink_none":        "The child has no link yet.",
			"helplink_hint":        "Sending a new link stops the old one working, including the one in earlier finding e-mails.",
			"mail_no_child":        "fill in the child's e-mail first",
			"digest_requests":      "The child asked for help:",
			"digest_week":          "Summary for %s–%s",
			"digest_open_findings": "Open Findings",
			"banner_more":          "+%d more",
			"hl_where":             "You'll find the current link in the newest e-mail from Guardian with the subject \"Guardian: when you need help\".",
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
			"model_empty":          "odpověď byla prázdná",
			"sit_title":            "Situace",
			"sit_intro":            "Co Guardian hlídá a co udělá, když to najde. Popis dostane model přesně tak, jak ho napíšeš.",
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
			"sit_off":              "vypnuto",
			"sit_incomplete":       "Vyplň název a popis.",
			"fin_title":            "Nálezy",
			"fin_intro":            "Co Guardian našel. Psané tak, aby to mohlo číst i dítě.",
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
			"child_intro":          "Guardian si v jednom z tvých chatů něčeho všiml. Nemusíš nic dělat.",
			"child_button":         "Potřebuju pomoc",
			"child_button_hint":    "Jestli s tím chceš pomoc, zmáčkni tlačítko: rodiče dostanou upozornění, že potřebuješ pomoc, a uvidí, co k tomu napíšeš.",
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
			"f_parent_hint":        "Každou adresu na nový řádek nebo odděl čárkou.",
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
			"kid_title":            "Guardian",
			"kid_found":            "Guardian si všiml: %s v chatu %s.",
			"kid_confirm":          "Ano, potřebuju pomoc",
			"kid_message":          "Chceš k tomu něco napsat?",
			"kid_thanks":           "Díky. Rodiče teď vědí, že potřebuješ pomoc.",
			"kid_expired":          "Tenhle odkaz už neplatí.",
			"kid_already":          "Žádost o pomoc už odešla %s. Rodiče o ní vědí.",
			"hello":                "Ahoj,",
			"kid_nothing_yet":      "Zatím se nic nestalo. Až zmáčkneš tlačítko, rodiče dostanou upozornění na telefon.",
			"kid_optional":         "Nepovinné. Můžeš napsat, co se děje nebo jak ti je.",
			"kid_resolved":         "Rodiče to označili jako vyřešené.",
			"push_test":            "Zkušební upozornění.",
			"push_test_button":     "Poslat zkušební upozornění",
			"push_sent":            "Zkušební upozornění je na cestě.",
			"this_device":          "toto zařízení",
			"notif_on_here":        "Upozornění jsou na tomto zařízení zapnutá.",
			"notif_home_screen":    "Na iPhonu fungují upozornění jen z plochy: Sdílet → Přidat na plochu, pak otevři Guardiana z plochy.",
			"mail_child_failed":    "E-mail dítěti neodešel: %s",
			"bad_address":          "„%s“ není platná e-mailová adresa.",
			"child_parents_know":   "Rodiče o tom už vědí.",
			"child_other_intro":    "Kdyby se dělo něco jiného:",
			"child_other_link":     "Potřebuju pomoc s něčím jiným",
			"help_request":         "Dítě žádá o pomoc",
			"hl_intro":             "Tady můžeš rodičům kdykoli dát vědět, že potřebuješ pomoc. S čímkoli, i s něčím, co Guardian nenašel.",
			"hl_field":             "Co se děje?",
			"hl_button":            "Potřebuju pomoc",
			"helplink_subject":     "Guardian: když budeš potřebovat pomoc",
			"helplink_mail_intro":  "Tohle je tvůj odkaz, kterým můžeš rodičům kdykoli dát vědět, že potřebuješ pomoc. S čímkoli, i s něčím, co Guardian nenašel. Rodiče dostanou upozornění na telefon.",
			"helplink_valid":       "Odkaz platí do %s.",
			"helplink_title":       "Odkaz pro pomoc",
			"helplink_send":        "Poslat dítěti odkaz pro pomoc",
			"helplink_none":        "Dítě zatím odkaz nemá.",
			"helplink_hint":        "Když dítěti pošleš nový odkaz, starý přestane platit, i ten v dřívějších e-mailech o nálezech.",
			"mail_no_child":        "nejdřív vyplň e-mail dítěte",
			"digest_requests":      "Dítě žádalo o pomoc:",
			"digest_week":          "Přehled za týden %s–%s",
			"digest_open_findings": "Otevřít Nálezy",
			"banner_more":          "+%d další",
			"hl_where":             "Platný odkaz najdeš v nejnovějším e-mailu od Guardiana s předmětem „Guardian: když budeš potřebovat pomoc“.",
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
	{Name: "Kontakt od cizího dospělého", Sensitivity: "medium", Action: "alert",
		Description: "Někdo, koho dítě zná jen z internetu, mu lichotí, vyptává se na věk, vzhled, samotu doma nebo vztahy, žádá fotky, nabízí dárky či peníze, chce přejít do jiné aplikace nebo žádá, aby to dítě nikomu neříkalo. Nepočítá se: spolužáci, příbuzní, trenér, který řeší trénink, běžné domlouvání ve hře bez osobních otázek."},
	{Name: "Vydírání intimními fotkami", Sensitivity: "medium", Action: "alert",
		Description: "Někdo vyhrožuje, že pošle dál nebo zveřejní intimní nebo upravené fotky či video dítěte (i pomocí AI), pokud nezaplatí, nepošle další nebo neudělá, co chce, často s časovým tlakem. Nepočítá se: kamarádi, kteří si bez výhrůžky dělají legraci z trapné fotky."},
	{Name: "Nátlak na intimní fotky", Sensitivity: "medium", Action: "alert",
		Description: "Někdo dítě opakovaně přemlouvá, aby poslalo nahé nebo intimní fotky, nebo se v chatu takové fotky dítěte či spolužáka šíří nebo vyžadují. Nepočítá se: fotky v plavkách z dovolené, memy, povídání o sexuální výchově ve škole."},
	{Name: "Opakované ponižování", Sensitivity: "low", Action: "digest",
		Description: "Jeden nebo víc lidí dítě opakovaně a jednostranně uráží, zesměšňuje nebo mu nadává (vzhled, rodina, původ) a dítě se brání nebo zmlkne. Nepočítá se: vzájemné pošťuchování a sprostá slova, která obě strany používají v legraci, herní pošťuchování, jednorázová hádka, která se vyřešila."},
	{Name: "Zesměšňování a falešné profily", Sensitivity: "low", Action: "digest",
		Description: "Ve skupině se sdílí fotka, video nebo upravený obrázek dítěte, aby se mu ostatní smáli, nebo se někdo za dítě vydává či mu založil falešný účet. Nepočítá se: společné fotky, kterým se děti smějí spolu, memy o známých lidech."},
	{Name: "Vyřazování ze skupiny", Sensitivity: "low", Action: "digest",
		Description: "Dítě je cíleně vyhazováno ze skupin, vzniká skupina „bez něj“, kde se mu smějí, nebo se ostatní domlouvají, že ho budou ignorovat nebo proti němu získají další. Nepočítá se: odebrání ze skupiny, která už splnila účel (výlet, projekt), menší skupina kamarádů na konkrétní akci."},
	{Name: "Výhrůžky násilím", Sensitivity: "low", Action: "alert",
		Description: "Konkrétní výhrůžka ublížením dítěti nebo někomu jinému (kdo, kde, kdy, třeba „po škole tě zmlátíme“), zmínky o zbrani nebo útoku na školu. Nepočítá se: přehánění („zabiju tě, jestli prohraješ“), hry, filmy, zjevný vtip, kterému se smějí obě strany."},
	{Name: "Dítě v nouzi", Sensitivity: "medium", Action: "alert",
		Description: "Dítě nebo jeho kamarád píše o sebepoškozování, o sebevraždě, že nechce žít, že je pro všechny přítěží, o extrémním hladovění, nebo ho k tomu někdo navádí. Nepočítá se: „umírám nudou“, „máma mě zabije“, únava a stres před písemkou, texty písní bez dalších náznaků."},
	{Name: "Schůzka s někým z internetu", Sensitivity: "medium", Action: "alert",
		Description: "Dítě se domlouvá na osobní schůzce s někým, koho zná jen z internetu, zvlášť když má přijít samo nebo to nikomu neříkat. Nepočítá se: schůzky se spolužáky, rodinou nebo lidmi z kroužku."},
	{Name: "Nebezpečné skupiny a výzvy", Sensitivity: "low", Action: "alert",
		Description: "Někdo dítě navádí, aby ublížilo sobě, zvířeti nebo jiným a natočilo to, k „výzvám“ s rizikem zranění, nebo ho láká do skupin, které obdivují útoky, násilí nebo extremismus. Nepočítá se: neškodné virální výzvy, povídání o zprávách nebo dějepisu."},
}

var defaultSituationsEN = []Situation{
	{Name: "Contact from an unknown adult", Sensitivity: "medium", Action: "alert",
		Description: "Someone the child knows only online flatters them, asks about age, looks, being home alone or relationships, asks for photos, offers gifts or money, wants to move to another app, or asks the child to keep it secret. Does not count: classmates, relatives, a coach talking about practice, ordinary in-game coordination without personal questions."},
	{Name: "Sextortion", Sensitivity: "medium", Action: "alert",
		Description: "Someone threatens to send on or publish intimate or edited photos or videos of the child (also made with AI) unless they pay, send more or do what is asked, often with time pressure. Does not count: friends joking about an embarrassing photo with no threat."},
	{Name: "Pressure for intimate photos", Sensitivity: "medium", Action: "alert",
		Description: "Someone repeatedly pressures the child to send nude or intimate photos, or such photos of the child or a classmate are shared or demanded in a chat. Does not count: holiday swimwear photos, memes, talk about sex education at school."},
	{Name: "Repeated humiliation", Sensitivity: "low", Action: "digest",
		Description: "One or more people repeatedly and one-sidedly insult, mock or swear at the child (looks, family, origin) while the child objects or goes quiet. Does not count: mutual teasing and swearing both sides use jokingly, gaming trash talk, a one-off argument that got resolved."},
	{Name: "Mockery and fake profiles", Sensitivity: "low", Action: "digest",
		Description: "A photo, video or edited image of the child is shared in a group so others laugh at them, or someone pretends to be the child or made a fake account of them. Does not count: group photos the kids laugh at together, memes about famous people."},
	{Name: "Pushed out of the group", Sensitivity: "low", Action: "digest",
		Description: "The child is deliberately removed from groups, a group \"without them\" mocks them, or others agree to ignore them or turn others against them. Does not count: removal from a group that served its purpose (a trip, a project), a smaller group of friends for one plan."},
	{Name: "Threats of violence", Sensitivity: "low", Action: "alert",
		Description: "A concrete threat to hurt the child or someone else (who, where, when, like \"we'll beat you up after school\"), mentions of a weapon or an attack on a school. Does not count: exaggeration (\"I'll kill you if you lose\"), games, films, an obvious joke both sides laugh at."},
	{Name: "The child is in distress", Sensitivity: "medium", Action: "alert",
		Description: "The child or a friend writes about self-harm, suicide, not wanting to live, being a burden to everyone, extreme starving, or someone urges them to it. Does not count: \"dying of boredom\", \"mum will kill me\", tiredness and stress before a test, song lyrics with no other signs."},
	{Name: "Meeting someone from the internet", Sensitivity: "medium", Action: "alert",
		Description: "The child arranges to meet in person someone they know only online, especially if asked to come alone or tell no one. Does not count: meeting classmates, family or people from a club."},
	{Name: "Dangerous groups and challenges", Sensitivity: "low", Action: "alert",
		Description: "Someone urges the child to hurt themselves, an animal or others and film it, to do risky \"challenges\", or draws them into groups that admire attacks, violence or extremism. Does not count: harmless viral challenges, talking about the news or history."},
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

// date writes a full date: "2. 11. 2027" in Czech, "2 Nov 2027" in English.
func (l *lang) date(t time.Time) string {
	if l.code == "cs" {
		return fmt.Sprintf("%d. %d. %d", t.Day(), int(t.Month()), t.Year())
	}
	return t.Format("2 Jan 2006")
}

// stamp writes a weekday, date and time that stay right whenever they are
// read, as in a weekly digest: "ne 2. 11. 23:33", "Sun 2 Nov 23:33".
func (l *lang) stamp(t time.Time) string {
	if l.code == "cs" {
		days := [...]string{"ne", "po", "út", "st", "čt", "pá", "so"}
		return fmt.Sprintf("%s %d. %d. %s", days[t.Weekday()], t.Day(), int(t.Month()), t.Format("15:04"))
	}
	return t.Format("Mon 2 Jan 15:04")
}
