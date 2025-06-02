// This file is part of clipsync (C)2023 by Marco Paganini
// Please see http://github.com/marcopaganini/clipsync for details.

package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	mrand "math/rand"
	"time"
)

const cryptKeyLen = 32

// newGCM creates a new cipher and GCM with the given key, returning the
// gcm object returned by cipher.NewGCM.
func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != cryptKeyLen {
		return nil, fmt.Errorf("key must be exactly %d bytes long", cryptKeyLen)
	}
	c, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("error creating cipher: %v", err)
	}
	gcm, err := cipher.NewGCM(c)
	if err != nil {
		return nil, fmt.Errorf("error creating GCM: %v", err)
	}
	return gcm, nil
}

// encrypt returns a copy of the cleartext string encrypted with AES256.
func encrypt(cleartext string, key []byte) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	// Create a new random nonce.
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("error creating nonce: %v", err)
	}
	return string(gcm.Seal(nonce, nonce, []byte(cleartext), nil)), nil
}

// decrypt returns a copy of the decrypted ciphertext.
func decrypt(ciphertext string, key []byte) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if nonceSize > len(ciphertext) {
		return "", fmt.Errorf("nonce is longer than encrypted text")
	}
	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	cleartext, err := gcm.Open(nil, []byte(nonce), []byte(ciphertext), nil)
	if err != nil {
		return "", fmt.Errorf("error decrypting text: %v", err)
	}
	return string(cleartext), nil
}

// encrypt64 encrypts a copy of cleartext and returns a base64 encoded ciphertext.
func encrypt64(cleartext string, key []byte) (string, error) {
	ciphertext, err := encrypt(cleartext, key)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString([]byte(ciphertext)), nil
}

// decrypt decrypts a base64 encoded ciphertext and returns the plain cleartext.
func decrypt64(ciphertext string, key []byte) (string, error) {
	c, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("error decoding base64 encrypted text: %v", err)
	}
	cleartext, err := decrypt(string(c), key)
	if err != nil {
		return "", err
	}
	return cleartext, nil
}

// createPassword creates a 32-byte random password.
func createPassword() []byte {
	charset := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789~!@#$%^&*()-_+="
	ret := [cryptKeyLen]byte{}

	mrand.Seed(time.Now().UnixNano())

	clen := len(charset)
	for i := 0; i < cryptKeyLen; i++ {
		ret[i] = charset[mrand.Intn(clen)]
	}
	return ret[0:cryptKeyLen]
}
