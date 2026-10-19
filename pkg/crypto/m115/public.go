package m115

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
)

type Key [16]byte

func GenerateKey() Key {
	key := Key{}
	_, _ = io.ReadFull(rand.Reader, key[:])
	return key
}

func Encode(input []byte, key Key) (output string) {
	// Prepare buffer
	buf := make([]byte, 16+len(input))
	// Copy key and data to buffer
	copy(buf, key[:])
	copy(buf[16:], input)
	// XOR encode
	xorTransform(buf[16:], xorDeriveKey(key[:], 4))
	reverseBytes(buf[16:])
	xorTransform(buf[16:], xorClientKey)
	// Encrypt and encode
	output = base64.StdEncoding.EncodeToString(rsaEncrypt(buf))
	return
}

func Decode(input string, key Key) (output []byte, err error) {
	// Base64 decode
	data, err := base64.StdEncoding.DecodeString(input)
	if err != nil {
		return
	}
	if len(data) == 0 || len(data)%_KeyLength != 0 {
		return nil, errors.New("m115: invalid RSA block length")
	}
	// RSA decrypt
	data = rsaDecrypt(data)
	if len(data) < len(key) {
		return nil, errors.New("m115: decoded data is shorter than key")
	}
	// XOR decode
	output = make([]byte, len(data)-16)
	copy(output, data[16:])
	xorTransform(output, xorDeriveKey(data[:16], 12))
	reverseBytes(output)
	xorTransform(output, xorDeriveKey(key[:], 4))
	return
}
