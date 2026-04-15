package docker

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
)

type GuacToken struct {
	Connection GuacConnection `json:"connection"`
}

type GuacConnection struct {
	Type     string              `json:"type"`
	Settings GuacConnectionSettings `json:"settings"`
}

type GuacConnectionSettings struct {
	Hostname     string `json:"hostname"`
	Username     string `json:"username,omitempty"`
	Password     string `json:"password,omitempty"`
	Port         string `json:"port"`
	DisableCopy  string `json:"disable-copy"`
	DisablePaste string `json:"disable-paste"`
}

func GenerateGuacToken(connType, hostname, username, password string, port int, encryptionKey string) (string, error) {
	token := GuacToken{
		Connection: GuacConnection{
			Type: connType,
			Settings: GuacConnectionSettings{
				Hostname:     hostname,
				Username:     username,
				Password:     password,
				Port:         fmt.Sprintf("%d", port),
				DisableCopy:  "false",
				DisablePaste: "false",
			},
		},
	}

	tokenJSON, err := json.Marshal(token)
	if err != nil {
		return "", err
	}

	key := []byte(encryptionKey)
	if len(key) < 32 {
		padded := make([]byte, 32)
		copy(padded, key)
		key = padded
	} else {
		key = key[:32]
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	// PKCS7 padding
	blockSize := block.BlockSize()
	padding := blockSize - len(tokenJSON)%blockSize
	padded := make([]byte, len(tokenJSON)+padding)
	copy(padded, tokenJSON)
	for i := len(tokenJSON); i < len(padded); i++ {
		padded[i] = byte(padding)
	}

	iv := make([]byte, blockSize)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return "", err
	}

	mode := cipher.NewCBCEncrypter(block, iv)
	encrypted := make([]byte, len(padded))
	mode.CryptBlocks(encrypted, padded)

	data := map[string]string{
		"iv":    base64.StdEncoding.EncodeToString(iv),
		"value": base64.StdEncoding.EncodeToString(encrypted),
	}

	dataJSON, err := json.Marshal(data)
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(dataJSON), nil
}
