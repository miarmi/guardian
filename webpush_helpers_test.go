package guardian

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/hkdf"
	"crypto/sha256"
	"errors"
	"math/big"
)

// decryptPush is the browser's side, for tests.
func decryptPush(body []byte, ua *ecdh.PrivateKey, auth []byte) ([]byte, error) {
	if len(body) < 21 {
		return nil, errors.New("short")
	}
	salt, idlen := body[:16], int(body[20])
	asPub, err := ecdh.P256().NewPublicKey(body[21 : 21+idlen])
	if err != nil {
		return nil, err
	}
	shared, err := ua.ECDH(asPub)
	if err != nil {
		return nil, err
	}
	info := append(append([]byte("WebPush: info\x00"), ua.PublicKey().Bytes()...), asPub.Bytes()...)
	ikm, _ := hkdf.Key(sha256.New, shared, auth, string(info), 32)
	cek, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, body[21+idlen:], nil)
	if err != nil {
		return nil, err
	}
	return bytes.TrimRight(plain, "\x00")[:len(bytes.TrimRight(plain, "\x00"))-1], nil
}

// verifyES256 checks a VAPID token's signature, for tests.
func verifyES256(token string, pub *ecdsa.PublicKey) bool {
	i := bytes.LastIndexByte([]byte(token), '.')
	if i < 0 {
		return false
	}
	sig, err := b64.DecodeString(token[i+1:])
	if err != nil || len(sig) != 64 {
		return false
	}
	digest := sha256.Sum256([]byte(token[:i]))
	return ecdsa.Verify(pub, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:]))
}
