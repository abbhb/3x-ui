package subscriptioncompat

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"os"
	"time"
)

var (
	ErrEncryptedUnavailable = errors.New("encrypted_data_unavailable")
	ErrInvalidSnapshot      = errors.New("invalid_external_snapshot")
	ErrInvalid              = errors.New("invalid_subscription_configuration")
	ErrConflict             = errors.New("subscription_configuration_conflict")
	ErrNotFound             = errors.New("subscription_resource_not_found")
	ErrBudget               = errors.New("external_projection_limit_exceeded")
	ErrCascade              = errors.New("cascade_entry_unavailable")
	ErrMihomoRequired       = errors.New("cascade_requires_mihomo")
)

func externalKey() ([]byte, error) {
	secret := []byte(os.Getenv("XUI_EXTERNAL_SUBSCRIPTION_ENCRYPTION_KEY"))
	if len(secret) < 32 {
		return nil, ErrEncryptedUnavailable
	}
	key, err := hkdf.Key(sha256.New, secret, []byte("marzban/external-subscriptions/v1"), "encrypted-upstream-clash-material", 32)
	if err != nil {
		return nil, ErrEncryptedUnavailable
	}
	return key, nil
}

// Fernet and its HKDF domain match the source, allowing atomic ciphertext import.
func Encrypt(kind string, plain []byte) (string, error) {
	key, err := externalKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key[16:])
	if err != nil {
		return "", ErrEncryptedUnavailable
	}
	p := append([]byte(kind+"\x00"), plain...)
	pad := aes.BlockSize - len(p)%aes.BlockSize
	p = append(p, bytes.Repeat([]byte{byte(pad)}, pad)...)
	token := make([]byte, 25+len(p)+32)
	token[0] = 0x80
	binary.BigEndian.PutUint64(token[1:9], uint64(time.Now().Unix()))
	if _, err = rand.Read(token[9:25]); err != nil {
		return "", ErrEncryptedUnavailable
	}
	cipher.NewCBCEncrypter(block, token[9:25]).CryptBlocks(token[25:len(token)-32], p)
	mac := hmac.New(sha256.New, key[:16])
	mac.Write(token[:len(token)-32])
	copy(token[len(token)-32:], mac.Sum(nil))
	return base64.URLEncoding.EncodeToString(token), nil
}

func Decrypt(kind, encoded string) ([]byte, error) {
	key, err := externalKey()
	if err != nil {
		return nil, err
	}
	token, err := base64.URLEncoding.DecodeString(encoded)
	if err != nil || len(token) < 73 || token[0] != 0x80 || (len(token)-57)%aes.BlockSize != 0 {
		return nil, ErrEncryptedUnavailable
	}
	mac := hmac.New(sha256.New, key[:16])
	mac.Write(token[:len(token)-32])
	if !hmac.Equal(mac.Sum(nil), token[len(token)-32:]) {
		return nil, ErrEncryptedUnavailable
	}
	block, err := aes.NewCipher(key[16:])
	if err != nil {
		return nil, ErrEncryptedUnavailable
	}
	p := make([]byte, len(token)-57)
	cipher.NewCBCDecrypter(block, token[9:25]).CryptBlocks(p, token[25:len(token)-32])
	pad := int(p[len(p)-1])
	if pad < 1 || pad > aes.BlockSize || !bytes.Equal(p[len(p)-pad:], bytes.Repeat([]byte{byte(pad)}, pad)) {
		return nil, ErrEncryptedUnavailable
	}
	p = p[:len(p)-pad]
	prefix := []byte(kind + "\x00")
	if !bytes.HasPrefix(p, prefix) {
		return nil, ErrEncryptedUnavailable
	}
	return p[len(prefix):], nil
}
