package guardian

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

const (
	// settingRemoved remembers across restarts that the phone removed the device.
	settingRemoved = "whatsapp_removed_by_phone"
	deviceName     = "Guardian" // how the linked device is named on the phone
)

// whatsapp is the WhatsApp connector: one linked device, kept in secrets/.
type whatsapp struct {
	st        *store
	container *sqlstore.Container

	mu     sync.Mutex
	client *whatsmeow.Client
	state  waState
	number string // E.164 digits of the linked phone, "" if unknown
	qr     string // current QR code payload while pairing
	qrExp  time.Time
	cancel context.CancelFunc
}

func newWhatsApp(ctx context.Context, secretsDir string, st *store) (*whatsapp, error) {
	if err := ensureDir(secretsDir); err != nil {
		return nil, err
	}
	wastore.SetOSInfo(deviceName, [3]uint32{1, 0, 0})
	// With an unknown platform the phone ignores the name and shows "Other
	// device" ("Jiné zařízení"); as a desktop app it shows just "Guardian".
	wastore.DeviceProps.PlatformType = waCompanionReg.DeviceProps_DESKTOP.Enum()
	wastore.DeviceProps.RequireFullSync = boolPtr(true)
	container, err := sqlstore.New(ctx, "sqlite3",
		"file:"+secretsDir+"/whatsapp.db?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000", waLog.Noop)
	if err != nil {
		return nil, fmt.Errorf("session store: %w", err)
	}
	w := &whatsapp{st: st, container: container, state: waNotConnected}
	if v, _ := st.Setting(ctx, settingRemoved); v == "1" {
		w.state = waPhoneRemoved
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("session store: %w", err)
	}
	if device.ID != nil {
		// Already linked: reconnect in the background.
		w.client = w.newClient(device)
		if err := w.client.Connect(); err != nil {
			log.Printf("whatsapp: reconnect: %v", err)
		}
		w.state = waConnected
		w.number = device.ID.User
	}
	return w, nil
}

func (w *whatsapp) newClient(device *wastore.Device) *whatsmeow.Client {
	c := whatsmeow.NewClient(device, waLog.Stdout("whatsapp", "WARN", false))
	c.AddEventHandler(w.handle)
	return c
}

func (w *whatsapp) Status() waStatus {
	w.mu.Lock()
	defer w.mu.Unlock()
	return waStatus{State: w.state, Number: w.number, QR: w.qr, QRExpires: w.qrExp}
}

// StartPairing puts a QR code on offer. Safe to call while already pairing.
// The lock is not held while connecting, so the pages' status polls and the
// event handler never wait on the network.
func (w *whatsapp) StartPairing(ctx context.Context) error {
	w.mu.Lock()
	switch w.state {
	case waConnected:
		w.mu.Unlock()
		return errors.New("already connected")
	case waPairing:
		w.mu.Unlock()
		return nil
	}
	device := w.container.NewDevice()
	client := w.newClient(device)
	qrCtx, cancel := context.WithCancel(context.Background())
	qrChan, err := client.GetQRChannel(qrCtx)
	if err != nil {
		w.mu.Unlock()
		cancel()
		return err
	}
	prev := w.state
	w.client, w.cancel, w.state, w.qr = client, cancel, waPairing, ""
	w.mu.Unlock()

	if err := client.Connect(); err != nil {
		w.mu.Lock()
		if w.client == client { // not superseded meanwhile: back to how it was
			w.client, w.cancel, w.state = nil, nil, prev
		}
		w.mu.Unlock()
		cancel()
		return err
	}
	go w.readQR(client, qrChan)
	return nil
}

// readQR follows the pairing: each new code is put on offer; a timeout or
// error ends the pairing so the page can offer a retry.
func (w *whatsapp) readQR(client *whatsmeow.Client, qrChan <-chan whatsmeow.QRChannelItem) {
	for item := range qrChan {
		w.mu.Lock()
		if w.client != client { // superseded
			w.mu.Unlock()
			return
		}
		switch item.Event {
		case "code":
			w.qr = item.Code
			w.qrExp = time.Now().Add(item.Timeout)
		case whatsmeow.QRChannelSuccess.Event:
			w.qr, w.qrExp = "", time.Time{}
			// events.Connected finishes the job.
		default:
			// timeout or error: back to square one, the page offers a retry.
			log.Printf("whatsapp: pairing ended: %s %v", item.Event, item.Error)
			cancel := w.cancel
			w.qr, w.state, w.client, w.cancel = "", waNotConnected, nil, nil
			w.mu.Unlock()
			if cancel != nil {
				cancel()
			}
			client.Disconnect()
			return
		}
		w.mu.Unlock()
	}
}

// Disconnect logs the Guardian device out of the child's WhatsApp.
func (w *whatsapp) Disconnect(ctx context.Context) error {
	w.mu.Lock()
	client := w.client
	cancel := w.cancel
	w.client, w.cancel, w.qr = nil, nil, ""
	w.state = waNotConnected
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	_ = w.st.SetSetting(ctx, settingRemoved, "0")
	if client == nil {
		return nil
	}
	if client.IsLoggedIn() {
		if err := client.Logout(ctx); err != nil {
			client.Disconnect()
			return err
		}
	} else {
		client.Disconnect()
	}
	return nil
}

func (w *whatsapp) Close() {
	w.mu.Lock()
	client, cancel := w.client, w.cancel
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if client != nil {
		client.Disconnect()
	}
	w.container.Close()
}

// eventTimeout bounds the database work for one event; a history sync gets
// it per conversation, since one sync can hold thousands of messages.
const eventTimeout = 30 * time.Second

// handle is whatsmeow's event handler.
func (w *whatsapp) handle(evt any) {
	if e, ok := evt.(*events.HistorySync); ok {
		w.saveHistory(e)
		return
	}
	ctx, cancelCtx := context.WithTimeout(context.Background(), eventTimeout)
	defer cancelCtx()
	switch e := evt.(type) {
	case *events.PairSuccess:
		w.mu.Lock()
		w.number = e.ID.User
		w.mu.Unlock()
	case *events.Connected:
		w.mu.Lock()
		w.state, w.qr = waConnected, ""
		if w.client != nil && w.client.Store.ID != nil {
			w.number = w.client.Store.ID.User
		}
		w.mu.Unlock()
		_ = w.st.SetSetting(ctx, settingRemoved, "0")
		log.Print("whatsapp: connected")
	case *events.LoggedOut:
		// The child removed the device on the phone; whatsmeow drops the session.
		w.mu.Lock()
		client := w.client
		w.client, w.qr, w.state = nil, "", waPhoneRemoved
		w.mu.Unlock()
		_ = w.st.SetSetting(ctx, settingRemoved, "1")
		if client != nil {
			go client.Disconnect()
		}
		log.Print("whatsapp: device removed by the phone")
	case *events.Message:
		w.saveMessage(ctx, e)
	}
}

// currentClient returns the current client, nil when there is none.
func (w *whatsapp) currentClient() *whatsmeow.Client {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.client
}

// saveHistory keeps the messages of a history sync.
func (w *whatsapp) saveHistory(e *events.HistorySync) {
	client := w.currentClient()
	if client == nil {
		return
	}
	n := 0
	for _, conv := range e.Data.GetConversations() {
		n += w.saveConversation(client, conv)
	}
	log.Printf("whatsapp: history sync, %d messages", n)
}

// saveConversation keeps one conversation of a history sync and returns how
// many of its messages could be read.
func (w *whatsapp) saveConversation(client *whatsmeow.Client, conv *waHistorySync.Conversation) int {
	chatJID, err := types.ParseJID(conv.GetID())
	if err != nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), eventTimeout)
	defer cancel()
	name := conv.GetName()
	if name == "" {
		name = conv.GetDisplayName()
	}
	_ = w.st.EnsureChat(ctx, chatJID.String(), name, chatJID.Server == types.GroupServer, name != "")
	n := 0
	for _, hm := range conv.GetMessages() {
		msg, err := client.ParseWebMessage(chatJID, hm.GetMessage())
		if err != nil {
			continue
		}
		w.saveMessage(ctx, msg)
		n++
	}
	return n
}

func (w *whatsapp) saveMessage(ctx context.Context, e *events.Message) {
	kind, text := describe(e.Message)
	if kind == "" {
		return
	}
	chat := e.Info.Chat.ToNonAD()
	sender := e.Info.Sender.ToNonAD()
	m := Message{
		ID:         string(e.Info.ID),
		Chat:       chat.String(),
		Sender:     sender.String(),
		SenderName: e.Info.PushName,
		FromMe:     e.Info.IsFromMe,
		At:         e.Info.Timestamp,
		Kind:       kind,
		Text:       text,
	}
	if err := w.st.SaveMessage(ctx, m); err != nil {
		log.Printf("whatsapp: save: %v", err)
		return
	}
	w.rememberChat(ctx, e, chat)
}

// rememberChat stores a name for the chat when one can be found: the group's
// subject, the contact's name, or the sender's push name for a direct chat.
func (w *whatsapp) rememberChat(ctx context.Context, e *events.Message, chat types.JID) {
	isGroup := chat.Server == types.GroupServer
	if known, _ := w.st.ChatName(ctx, chat.String()); known != "" {
		return
	}
	client := w.currentClient()
	name := ""
	if client != nil {
		if isGroup {
			if info, err := client.GetGroupInfo(ctx, chat); err == nil {
				name = info.Name
			}
		} else if c, err := client.Store.Contacts.GetContact(ctx, chat); err == nil && c.Found {
			name = firstNonEmpty(c.FullName, c.FirstName, c.PushName, c.BusinessName)
		}
	}
	if name == "" && !isGroup && !e.Info.IsFromMe {
		name = e.Info.PushName
	}
	if name == "" && !isGroup {
		name = "+" + chat.User
	}
	_ = w.st.EnsureChat(ctx, chat.String(), name, isGroup, false)
}

// describe tells what kind of message this is and the text worth keeping.
// Media is reduced to its caption; empty kind means nothing to keep.
func describe(m *waE2E.Message) (kind, text string) {
	switch {
	case m == nil:
		return "", ""
	case m.GetConversation() != "":
		return "text", m.GetConversation()
	case m.GetExtendedTextMessage() != nil:
		return "text", m.GetExtendedTextMessage().GetText()
	case m.GetImageMessage() != nil:
		return "image", m.GetImageMessage().GetCaption()
	case m.GetVideoMessage() != nil:
		return "video", m.GetVideoMessage().GetCaption()
	case m.GetAudioMessage() != nil:
		return "audio", ""
	case m.GetStickerMessage() != nil:
		return "sticker", ""
	case m.GetDocumentMessage() != nil:
		return "document", firstNonEmpty(m.GetDocumentMessage().GetCaption(), m.GetDocumentMessage().GetFileName())
	case m.GetReactionMessage() != nil:
		return "reaction", m.GetReactionMessage().GetText()
	case m.GetProtocolMessage() != nil, m.GetSenderKeyDistributionMessage() != nil:
		return "", ""
	}
	return "other", ""
}

func boolPtr(b bool) *bool { return &b }
