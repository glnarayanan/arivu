package secrets

import (
	"crypto/sha256"
	"io"

	"golang.org/x/crypto/hkdf"
)

const keyLength = 32

func EncryptionKey(secretKey string) ([]byte, error) {
	reader := hkdf.New(sha256.New, []byte(secretKey), []byte("arivu:v2:secret-encryption"), []byte("aes-256-gcm"))
	key := make([]byte, keyLength)
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, err
	}
	return key, nil
}
