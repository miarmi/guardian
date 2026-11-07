package guardian

import "net/http"

// The Platformy page (criterion 31): every platform children use, with
// what Guardian can do there and, where it can't, what to do instead.
// Based on research in November 2026 (README, "Platforms").

type platformState string

const (
	platConnected    platformState = "connected"     // Připojeno
	platNotConnected platformState = "not_connected" // can be connected here, isn't yet
	platEmail        platformState = "email"         // Přes rodičovské e-maily
	platLater        platformState = "later"         // Šlo by, zatím ne
	platNo           platformState = "no"            // Nejde připojit
)

// platform is one row; Why says why in one sentence, Do what to do.
type platform struct {
	Name  string
	State platformState
	Why   string
	Do    string
	Link  string // where to act in Guardian, if anywhere
}

// platformText is a platform's fixed words in each language.
type platformText struct {
	name  string
	state platformState
	why   map[string]string
	do    map[string]string
}

var platformTexts = []platformText{
	{"Discord", platEmail,
		map[string]string{
			"cs": "Zprávy Discord nikomu zvenku nepustí. Family Center ale posílá rodičům týdenní e-mail: noví přátelé, servery a s kým si dítě psalo.",
			"en": "Discord lets no outside app read messages. Its Family Center e-mails parents every week: new friends, servers and who the child talked to."},
		map[string]string{
			"cs": "Zapni Family Center (Discord → Nastavení → Family Center) a v Guardianovi čtení rodičovských e-mailů.",
			"en": "Turn on Family Center (Discord → Settings → Family Center) and reading parental e-mails in Guardian."}},
	{"Roblox", platEmail,
		map[string]string{
			"cs": "Chat v Robloxu běží uvnitř her a nemá žádné rozhraní. Rodičovský účet posílá e-maily o útratách a žádostech o schválení přátel.",
			"en": "Roblox chat runs inside games and has no interface. A parent account e-mails about spending and requests to approve friends."},
		map[string]string{
			"cs": "Propoj svůj rodičovský účet s účtem dítěte (Roblox → Nastavení → Rodičovská kontrola).",
			"en": "Link your parent account to the child's (Roblox → Settings → Parental Controls)."}},
	{"Fortnite (Epic Games)", platEmail,
		map[string]string{
			"cs": "Chat ve hře není dostupný. Epic posílá týdenní e-mail o tom, kolik dítě hrálo.",
			"en": "In-game chat isn't available. Epic e-mails a weekly report of how much the child played."},
		map[string]string{
			"cs": "Zapni týdenní reporty o hraní v rodičovské kontrole Epic Games.",
			"en": "Turn on weekly time reports in Epic Games parental controls."}},
	{"Instagram", platLater,
		map[string]string{
			"cs": "Instagram nemá oficiální cestu ke zprávám. Neoficiální by šla, ale účet dítěte by mohl být zablokovaný.",
			"en": "Instagram has no official way to messages. An unofficial one exists, but the child's account could be locked."},
		map[string]string{
			"cs": "Zatím: dohled pro teenagery v aplikaci Instagram a rozhovor s dítětem.",
			"en": "For now: Instagram's teen supervision in the app, and talking with the child."}},
	{"Messenger", platLater,
		map[string]string{
			"cs": "Stejně jako Instagram: jen neoficiálně, s rizikem zablokování účtu.",
			"en": "Like Instagram: only unofficially, with a risk of the account being locked."},
		map[string]string{
			"cs": "Zatím: dohled v aplikaci Messenger.",
			"en": "For now: supervision in the Messenger app."}},
	{"Telegram", platLater,
		map[string]string{
			"cs": "Telegram má oficiální rozhraní a propojení přes QR kód by šlo. Zatím není postavené.",
			"en": "Telegram has an official interface and a QR link would work. It isn't built yet."},
		map[string]string{"cs": "Dá se doplnit.", "en": "Can be added."}},
	{"Signal", platLater,
		map[string]string{
			"cs": "Signal jde propojit jako další zařízení, podobně jako WhatsApp. Zatím není postavené.",
			"en": "Signal can be linked as another device, like WhatsApp. It isn't built yet."},
		map[string]string{"cs": "Dá se doplnit.", "en": "Can be added."}},
	{"Bakaláři", platLater,
		map[string]string{
			"cs": "Zprávy Komens jdou číst s přihlášením dítěte, zatím to není postavené. Šikany tam bývá málo, je to hlavně komunikace se školou.",
			"en": "Komens messages can be read with the child's login. There is little bullying there; it is mostly school messages."},
		map[string]string{"cs": "Dá se doplnit.", "en": "Can be added."}},
	{"Snapchat", platNo,
		map[string]string{
			"cs": "Snapchat nikomu zvenku zprávy nepustí a účty s cizími aplikacemi zamyká.",
			"en": "Snapchat lets no outside app read messages and locks accounts that use one."},
		map[string]string{
			"cs": "Family Center v aplikaci ukáže nové přátele a s kým si dítě píše.",
			"en": "Family Center in the app shows new friends and who the child talks to."}},
	{"TikTok", platNo,
		map[string]string{
			"cs": "Zprávy ani komentáře nemají rozhraní.",
			"en": "Neither messages nor comments have an interface."},
		map[string]string{
			"cs": "Párování rodiny v aplikaci ukáže, koho dítě sleduje a kdo sleduje jeho.",
			"en": "Family Pairing in the app shows who the child follows and who follows them."}},
	{"Minecraft", platNo,
		map[string]string{
			"cs": "Chat běží na herních serverech a nemá rozhraní. Šel by číst jen na serveru, který provozuješ sám.",
			"en": "Chat runs on game servers and has no interface. It could only be read on a server you run yourself."},
		map[string]string{
			"cs": "V Microsoft Family nastav, s kým může dítě v Minecraftu mluvit.",
			"en": "In Microsoft Family, set who the child can talk to in Minecraft."}},
	{"Viber", platNo,
		map[string]string{"cs": "Viber nemá cestu k osobním zprávám.", "en": "Viber has no way to personal messages."},
		map[string]string{"cs": "Zatím jen rozhovor s dítětem.", "en": "For now, only talking with your child."}},
	{"PlayStation", platNo,
		map[string]string{"cs": "Zprávy nemají rozhraní.", "en": "Messages have no interface."},
		map[string]string{
			"cs": "V aplikaci PlayStation Family nastav, s kým může dítě komunikovat.",
			"en": "In the PlayStation Family app, set who the child can talk to."}},
	{"iMessage a SMS", platNo,
		map[string]string{
			"cs": "Apple zprávy z iPhonu nikomu nepustí.",
			"en": "Apple lets nothing read messages from an iPhone."},
		map[string]string{
			"cs": "Na iPhonu dítěte zapni schvalování nových kontaktů v Rodinném sdílení.",
			"en": "On the child's iPhone, turn on approval of new contacts in Family Sharing."}},
}

type platformsData struct {
	page
	Platforms []platform
	Steps     []phoneStep // criterion 40: what only the child's phone can set
	Checked   string      // when they were last checked, "" if never
}

func (a *App) platformsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	code := a.lang.code
	var list []platform
	// What Guardian connects itself comes first.
	wa := platform{Name: "WhatsApp", State: platNotConnected, Why: a.lang.T("plat_wa_why"), Link: "/"}
	if a.wa != nil && a.wa.Status().State == waConnected {
		wa.State = platConnected
	} else {
		wa.Do = a.lang.T("plat_wa_do")
	}
	list = append(list, wa)
	pm := platform{Name: a.lang.T("plat_mail"), State: platNotConnected, Why: a.lang.T("plat_mail_why"), Link: "/settings#gmail"}
	g := readGmailSettings(ctx, a.store, a.nt.secretsDir, a.dev != nil)
	failed := ""
	if a.an != nil {
		failed = a.an.gmailError()
	}
	switch {
	case g.Read && g.ready() && failed != "":
		pm.Do = a.lang.T("gmail_failed", failed) // not honest to call it connected
	case g.Read && g.ready():
		pm.State = platConnected
	default:
		pm.Do = a.lang.T("plat_mail_do")
	}
	list = append(list, pm)
	for _, p := range platformTexts {
		list = append(list, platform{Name: p.name, State: p.state, Why: p.why[code], Do: p.do[code]})
	}
	data := platformsData{page: a.page(), Platforms: list, Steps: phoneSteps[code]}
	if t := a.lastPhoneCheck(ctx); !t.IsZero() {
		data.Checked = a.lang.date(t)
	}
	a.render(w, "platforms", http.StatusOK, data)
}

// phoneStep is one setting Guardian can't make (criterion 40).
type phoneStep struct{ Title, Path, Why string }

// phoneSteps live on the child's phone or in the platforms' parent tools.
// Research, Nov 2026.
var phoneSteps = map[string][]phoneStep{
	"cs": {
		{"Apple iPhone: Bezpečnost komunikace", "Nastavení → Čas u obrazovky → (jméno dítěte) → Bezpečnost komunikace → zapnout",
			"Rozmaže nahé fotky ve Zprávách, AirDropu a FaceTimu a nabídne dítěti pomoc."},
		{"Apple iPhone: schvalování nových kontaktů", "Nastavení → Čas u obrazovky → (jméno dítěte) → Omezení komunikace → schvalovat nové kontakty",
			"Dítě pak nemůže psát nikomu novému bez tvého souhlasu."},
		{"Na Discordu: filtr citlivých médií", "Nastavení → Soukromí a bezpečnost → Filtr citlivých médií → Blokovat (od nepřátel i přátel)",
			"A vypni zprávy od lidí ze serverů."},
		{"Na Instagramu: účet pro teenagery", "Soukromý účet, zprávy jen od lidí, které dítě sleduje",
			"Dohled zapneš v Centru rodiny."},
		{"Na Robloxu: rodičovský účet", "Chat jen s přáteli (nebo vypnutý), schvalování nových přátel",
			"Nastavíš v rodičovském účtu, propojeném s účtem dítěte."},
	},
	"en": {
		{"Apple iPhone: Communication Safety", "Settings → Screen Time → (child's name) → Communication Safety → on",
			"Blurs nude photos in Messages, AirDrop and FaceTime and offers the child help."},
		{"Apple iPhone: approving new contacts", "Settings → Screen Time → (child's name) → Communication Limits → ask to approve new contacts",
			"The child can't text anyone new without your OK."},
		{"On Discord: sensitive media filter", "Settings → Privacy & Safety → Sensitive media filter → Block (non-friends and friends)",
			"And turn off messages from server members."},
		{"On Instagram: teen account", "Private account, messages only from people the child follows",
			"Turn on supervision in the Family Center."},
		{"On Roblox: parent account", "Chat with friends only (or off), approving new friends",
			"Set in a parent account linked to the child's."},
	},
}
