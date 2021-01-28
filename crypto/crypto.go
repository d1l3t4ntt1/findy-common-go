package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"io"

	"github.com/golang/glog"
	"github.com/lainio/err2"
	"github.com/lainio/err2/assert"
)

// Cipher is type for our block AES cipher
type Cipher struct {
	block  cipher.Block
	aesGCM cipher.AEAD
}

// NewCipher creates a new cipher with key 32-byte data given
func NewCipher(k []byte) *Cipher {
	assert.D.EqualInt(len(k), 32)

	defer err2.Catch(func(err error) {
		glog.Error(err)
	})

	newBlock, err := aes.NewCipher(k)
	err2.Check(err)

	// Create a new GCM - https://en.wikipedia.org/wiki/Galois/Counter_Mode
	// https://golang.org/pkg/crypto/cipher/#NewGCM
	newAesGCM, err := cipher.NewGCM(newBlock)
	err2.Check(err)

	return &Cipher{block: newBlock, aesGCM: newAesGCM}
}

// Encrypt is same as TryEncrypt but note used yet
func (c *Cipher) _(in []byte) (out []byte, err error) {
	defer err2.Return(&err)
	return c.TryEncrypt(in), nil
}

func (c *Cipher) TryEncrypt(in []byte) (out []byte) {
	nonce := make([]byte, c.aesGCM.NonceSize())
	err2.Empty.Try(io.ReadFull(rand.Reader, nonce))

	// We add it as a prefix to the encrypted data. The first nonce argument in
	// Seal is the prefix.
	return c.aesGCM.Seal(nonce, nonce, in, nil)
}

// Decrypt is same as TryDecrypt but note used yet
func (c *Cipher) _(in []byte) (out []byte, err error) {
	defer err2.Return(&err)
	return c.TryDecrypt(in), nil
}

func (c *Cipher) TryDecrypt(in []byte) (out []byte) {
	nonceSize := c.aesGCM.NonceSize()

	nonce, ciphertext := in[:nonceSize], in[nonceSize:]

	return err2.Bytes.Try(c.aesGCM.Open(nil, nonce, ciphertext, nil))
}
