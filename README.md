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

## What it does

- **WhatsApp connector.** Links to the child's WhatsApp as a companion device
  named *Guardian* (the same mechanism as WhatsApp Web), by scanning a QR code
  together. Message text, sender, chat and time go into a SQLite file; media
  are kept only as their kind and caption, never the file.
- **Analysis by an AI model you choose.** Once an hour the new messages, with
  earlier ones as context, go to the model you configure: Anthropic (Claude)
  or any OpenAI-compatible API, cloud or local. Your key stays in `secrets/`.
- **Ten situations from research.** The defaults follow the most common and
  most serious real cases (Czech school surveys, Linka bezpečí, NCMEC, Thorn,
  NSPCC, FBI): an unknown adult, sextortion, pressure for intimate photos,
  repeated humiliation, mockery and fake profiles, being pushed out of a
  group, threats of violence, a child in distress, meeting someone from the
  internet, dangerous groups and challenges. Each says what does *not*
  count (mutual banter, gaming trash talk, exaggeration), and you can change
  them or add your own.
- **Parents first, the child can ask for help.** Serious findings reach the
  parents at once by web push; the rest go into a Sunday digest. The child
  hears afterwards by e-mail, calmly, with a button "I need help" and a
  personal link to ask for help at any time, and always with contacts for
  help outside the family (in Czech: Linka bezpečí, E-Bezpečí, 116 123,
  school, 158/112).
- **Platforms, honestly.** A page lists the platforms children use and what
  Guardian can and can't see there. Discord, Roblox and Epic don't let
  anyone read chats, but their parental tools e-mail the parent: Guardian
  reads only those e-mails from your Gmail (read-only IMAP, nothing else is
  downloaded, nothing is marked or deleted) and turns them into signals.
- **E-mail** through Gmail, Microsoft 365 (Graph) or any SMTP server.

## What comes next

- More official connectors where they exist: Telegram, Signal, Bakaláři.
- Instagram, Messenger and Discord messages have no official access; the
  unofficial routes risk the child's account being locked and are left out
  for now.
- Easy install for other families: self-hosting, then one-click hosting.

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
- `GUARDIAN_PRACTICE=1` runs pretend services instead, for tests and demos:
  a WhatsApp (`/dev/whatsapp/*`), a model (`/dev/analysis/run`,
  `/dev/model/unreachable`), an outbox for e-mail and push (`/dev/outbox`,
  `/dev/digest/run`, `/dev/mail/unreachable`) and a Gmail
  (`/dev/gmail/message`, `/dev/gmail/unreachable`). These addresses exist
  only in this mode.
- `BASE_URL` is Guardian's own public address (for links in e-mails and
  notifications). The child's help links live under `/kid/`: if Guardian
  sits behind a login, let `/kid/` through it; those requests are checked
  with signed, expiring links.
- `GUARDIAN_LANG=cs` for Czech pages; English is the default. Adding a
  language is one table in `i18n.go`.
- The pages have **no login of their own**. Run Guardian behind a reverse
  proxy with authentication, on a private network, or both. It is a child's
  message archive; treat it accordingly.

## Use it as a library

```go
app, err := guardian.New(ctx, guardian.Config{DataDir: "data", Connect: true, Language: "cs"})
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
- [Atkinson Hyperlegible Next](https://www.brailleinstitute.org/freefont/) —
  the font, SIL Open Font License (`web/fonts-LICENSE.txt`).

Guardian is not affiliated with WhatsApp or Meta.

## License

MIT.
