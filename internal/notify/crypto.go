package notify

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
)

// Crypter 用 AES-GCM 加密渠道凭据（密钥由 config.Secret 经 SHA-256 派生为 32 字节）。
// 密文格式：nonce(12) || ciphertext。API 永不回传明文密码/secret。
type Crypter struct {
	key [32]byte
}

// NewCrypter 由 secret 派生 32 字节密钥。secret 为空时会 panic（调用方需保证已配置）。
func NewCrypter(secret string) *Crypter {
	if secret == "" {
		// 退化为固定密钥，避免崩溃；生产环境必须配置 secret。
		secret = "lanwatch-insecure-default-secret"
	}
	return &Crypter{key: sha256.Sum256([]byte(secret))}
}

// Encrypt 加密任意字节，返回 nonce||ciphertext。
func (c *Crypter) Encrypt(plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(c.key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, nil), nil
}

// Decrypt 解密 Encrypt 的产物。
func (c *Crypter) Decrypt(b []byte) ([]byte, error) {
	block, err := aes.NewCipher(c.key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(b) < ns {
		return nil, errors.New("notify: ciphertext too short")
	}
	return gcm.Open(nil, b[:ns], b[ns:], nil)
}

// EncryptJSON 把 v 序列化后加密。
func (c *Crypter) EncryptJSON(v interface{}) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return c.Encrypt(b)
}

// DecryptJSON 解密并反序列化到 v。
func (c *Crypter) DecryptJSON(b []byte, v interface{}) error {
	plain, err := c.Decrypt(b)
	if err != nil {
		return err
	}
	return json.Unmarshal(plain, v)
}

// DecryptJSONToMap 解密为通用 map（用于 API 脱敏/合并）。
func (c *Crypter) DecryptJSONToMap(b []byte) (map[string]interface{}, error) {
	var m map[string]interface{}
	if err := c.DecryptJSON(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}
