package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type encryptedStore struct {
	dir  string
	aead cipher.AEAD
}

func newEncryptedStore(dir, keyMaterial string) (*encryptedStore, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("buat security store dir: %w", err)
	}
	sum := sha256.Sum256([]byte(keyMaterial))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &encryptedStore{dir: dir, aead: aead}, nil
}

func (s *encryptedStore) readJSON(name string, dst interface{}) error {
	path := filepath.Join(s.dir, name+".enc")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	plain, err := s.decrypt(data)
	if err != nil {
		return err
	}
	return decodeJSON(plain, dst)
}

func (s *encryptedStore) writeJSON(name string, src interface{}) error {
	plain, err := encodeJSON(src)
	if err != nil {
		return err
	}
	enc, err := s.encrypt(plain)
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, name+".enc")
	return os.WriteFile(path, enc, 0600)
}

func (s *encryptedStore) appendEncryptedLine(name string, src interface{}) error {
	plain, err := encodeJSON(src)
	if err != nil {
		return err
	}
	enc, err := s.encrypt(plain)
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, name+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(enc, '\n'))
	return err
}

func (s *encryptedStore) decryptLine(line []byte) ([]byte, error) {
	return s.decrypt(line)
}

func (s *encryptedStore) encrypt(plain []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	sealed := s.aead.Seal(nil, nonce, plain, nil)
	buf := append(nonce, sealed...)
	out := make([]byte, base64.StdEncoding.EncodedLen(len(buf)))
	base64.StdEncoding.Encode(out, buf)
	return out, nil
}

func (s *encryptedStore) decrypt(data []byte) ([]byte, error) {
	data = trimSpace(data)
	raw := make([]byte, base64.StdEncoding.DecodedLen(len(data)))
	n, err := base64.StdEncoding.Decode(raw, data)
	if err != nil {
		return nil, err
	}
	raw = raw[:n]
	nonceSize := s.aead.NonceSize()
	if len(raw) < nonceSize {
		return nil, fmt.Errorf("ciphertext terlalu pendek")
	}
	nonce, ciphertext := raw[:nonceSize], raw[nonceSize:]
	return s.aead.Open(nil, nonce, ciphertext, nil)
}
