# Guardian

Guardian keeps a copy of your child's chats **at home, on your own server,
with your child's knowledge**, so that it can look for bullying and tell you
when something is wrong. Nothing is hidden: every connection shows up on the
child's phone as a linked device named *Guardian*, and the child can remove it
at any time.

It is built by a parent for one child first. If it is useful to you too, help
is welcome.

## Why

Chat platforms already archive everything a child writes. The problem is not
data, it is detection and a timely word to the parents. Commercial tools exist,
but they send the child's messages to someone else's cloud. Guardian runs on
your machine; the messages never leave it.

## What it does today (phase 1)

- **WhatsApp connector.** Links to the child's WhatsApp as a companion device
  (the same mechanism as WhatsApp Web), by scanning a QR code together.
- **Keeps messages.** Every incoming and outgoing message's text, sender, chat
  and time go into a SQLite file. Photos, videos, voice messages, stickers and
  documents are kept only as their kind and caption, never the file.
- **Shows that data flows.** A page lists the chats with message counts and
  the time of the last one. Message content is shown nowhere.

## What comes next

- Phase 2: analysis. An LLM looks for *patterns*, not single messages:
  repeated insults from the same people, exclusion from groups, threats,
  blackmail, contact from unknown adults, a change in the child's own tone.
  Output per chat: a 0–3 score with a short reason.
- Phase 3: notifications. A weekly digest for parents **and child** (the same
  document), and an immediate alert only for the worst: threats, sexual
  content, an unknown adult.
- More connectors: Discord (bot for servers), Messenger and Instagram
  (same companion-device approach), a drop box where the child can send a
  screenshot from any app.

## Run it

Requires Go 1.26 and a C compiler (SQLite).

```sh
go build -o guardian ./cmd/guardian
PORT=8080 DATA_DIR=./data GUARDIAN_CONNECT=1 TZ=Europe/Prague ./guardian
```

Open http://127.0.0.1:8080, press **Connect WhatsApp** and scan the code on
the child's phone under *Linked devices*.

- `DATA_DIR` holds `guardian.db` (the messages) and `secrets/whatsapp.db`
  (the login). Never copy `secrets/` to a second instance: WhatsApp logs one
  of them out.
- `GUARDIAN_CONNECT=1` allows the connector to run. Leave it unset for a test
  copy.
- The pages have **no login of their own**. Run Guardian behind a reverse
  proxy with authentication, on a private network, or both. It is a child's
  message archive; treat it accordingly.

## Use it as a library

```go
app, err := guardian.New(ctx, guardian.Config{DataDir: "data", Connect: true})
http.ListenAndServe("127.0.0.1:8080", app.Handler())
```

## Talk to the child first

Guardian is not a spy tool. Agree with the child what is collected, what the
parents see (only flagged chats, not everything), what triggers an alert, and
that nothing found leads to punishment, only to help. Without that agreement
the child opens a second account and the tool protects nobody.

## Built on

- [whatsmeow](https://github.com/tulir/whatsmeow) — the WhatsApp protocol,
  MPL-2.0.
- [go-qrcode](https://github.com/skip2/go-qrcode) — the pairing code.
- [go-sqlite3](https://github.com/mattn/go-sqlite3) — storage.

Guardian is not affiliated with WhatsApp or Meta.

## License

MIT.
