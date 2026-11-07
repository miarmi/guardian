package guardian

import (
	"context"
	"time"
)

// connector is what the pages need from a WhatsApp connection, real
// (whatsapp.go) or practice (practice.go).
type connector interface {
	Status() waStatus
	StartPairing(ctx context.Context) error
	Disconnect(ctx context.Context) error
	Close()
	// Privacy reads the child's WhatsApp privacy settings (criterion 39);
	// SetPrivacySafe sets them safely, only when the parents press the button.
	Privacy(ctx context.Context) (privacy, error)
	SetPrivacySafe(ctx context.Context) error
	// watch tells the connector whom to report strangers, photos and
	// privacy changes to, and who judges photos (criteria 37–39).
	watch(ev safetyEvents, judge photoJudge)
}

// waState is what the connector overview shows.
type waState string

const (
	waNotConnected waState = "not_connected"
	waPairing      waState = "pairing" // a QR code is on offer
	waConnected    waState = "connected"
	waPhoneRemoved waState = "phone_removed" // the child removed the device on the phone
)

// waStatus is a snapshot for the pages.
type waStatus struct {
	State     waState
	Number    string // E.164 digits of the linked phone, "" if unknown
	QR        string // the QR code's payload while pairing
	QRExpires time.Time
}
