package guardian

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// Web Push from Guardian itself (criterion 22): VAPID (RFC 8292) to say who
// sends, aes128gcm (RFC 8291) so only the browser can read the message. The
// push service in between (Apple, Google, Mozilla) sees only ciphertext.

var b64 = base64.RawURLEncoding

// loadVAPIDKey loads or makes the server's signing key, kept in secrets/.
func loadVAPIDKey(secretsDir string) (*ecdsa.PrivateKey, error) {
	path := filepath.Join(secretsDir, "vapid.pem")
	if data, err := os.ReadFile(path); err == nil {
		block, _ := pem.Decode(data)
		if block == nil {
			return nil, errors.New("vapid.pem is not PEM")
		}
		return x509.ParseECPrivateKey(block.Bytes)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	if err := ensureDir(secretsDir); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// vapidPublic is the key browsers need to subscribe (applicationServerKey).
func vapidPublic(key *ecdsa.PrivateKey) string {
	pub, err := key.PublicKey.ECDH()
	if err != nil {
		return ""
	}
	return b64.EncodeToString(pub.Bytes())
}

// pushMessage is what the service worker shows.
type pushMessage struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
}

var errGone = errors.New("the device is no longer subscribed")

var pushHTTP = &http.Client{Timeout: 30 * time.Second}

// sendPush delivers one message to one device. errGone means the device
// unsubscribed and should be forgotten.
func sendPush(ctx context.Context, key *ecdsa.PrivateKey, contact string, d PushDevice, m pushMessage) error {
	payload, _ := json.Marshal(m)
	body, err := encryptPush(payload, d.P256dh, d.Auth)
	if err != nil {
		return err
	}
	u, err := url.Parse(d.Endpoint)
	if err != nil || u.Scheme != "https" {
		return errors.New("bad push endpoint")
	}
	jwt, err := vapidJWT(key, u.Scheme+"://"+u.Host, contact)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", "86400")
	req.Header.Set("Urgency", "high")
	req.Header.Set("Authorization", "vapid t="+jwt+", k="+vapidPublic(key))
	resp, err := pushHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return errGone
	case resp.StatusCode/100 != 2:
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// vapidJWT signs the ES256 token that tells the push service who sends.
func vapidJWT(key *ecdsa.PrivateKey, audience, contact string) (string, error) {
	header := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, _ := json.Marshal(map[string]any{
		"aud": audience,
		"exp": time.Now().Add(12 * time.Hour).Unix(),
		"sub": contact,
	})
	signing := header + "." + b64.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signing + "." + b64.EncodeToString(sig), nil
}

// encryptPush is RFC 8291 with one record: salt, record size, the server's
// public key, then the AES-128-GCM ciphertext of the padded payload.
func encryptPush(payload []byte, p256dh, authSecret string) ([]byte, error) {
	uaPubBytes, err := b64.DecodeString(p256dh)
	if err != nil {
		return nil, errors.New("bad device key")
	}
	auth, err := b64.DecodeString(authSecret)
	if err != nil || len(auth) != 16 {
		return nil, errors.New("bad device auth")
	}
	curve := ecdh.P256()
	uaPub, err := curve.NewPublicKey(uaPubBytes)
	if err != nil {
		return nil, errors.New("bad device key")
	}
	asKey, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	shared, err := asKey.ECDH(uaPub)
	if err != nil {
		return nil, err
	}
	asPub := asKey.PublicKey().Bytes()
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	info := append(append([]byte("WebPush: info\x00"), uaPubBytes...), asPub...)
	ikm, err := hkdf.Key(sha256.New, shared, auth, string(info), 32)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain := append(append([]byte(nil), payload...), 0x02) // last (only) record
	sealed := gcm.Seal(nil, nonce, plain, nil)
	const recordSize = 4096
	out := make([]byte, 0, 16+4+1+len(asPub)+len(sealed))
	out = append(out, salt...)
	out = binary.BigEndian.AppendUint32(out, recordSize)
	out = append(out, byte(len(asPub)))
	out = append(out, asPub...)
	out = append(out, sealed...)
	return out, nil
}
