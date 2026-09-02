package notify

import (
	"bytes"
	"testing"
)

func TestCrypterRoundTrip(t *testing.T) {
	c := NewCrypter("LANWATCH_SECRET_test")
	plain := []byte(`{"password":"s3cr3t","secret":"SECxxx"}`)
	enc, err := c.Encrypt(plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if bytes.Equal(enc, plain) {
		t.Fatal("ciphertext equals plaintext")
	}
	dec, err := c.Decrypt(enc)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(dec, plain) {
		t.Fatalf("roundtrip mismatch: got %q", dec)
	}
}

func TestCrypterJSONRoundTrip(t *testing.T) {
	c := NewCrypter("secret")
	type cfg struct {
		Password string `json:"password"`
		Token    string `json:"token"`
	}
	in := cfg{Password: "pw", Token: "tk"}
	enc, err := c.EncryptJSON(in)
	if err != nil {
		t.Fatal(err)
	}
	var out cfg
	if err := c.DecryptJSON(enc, &out); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("json mismatch: %+v", out)
	}
}

func TestCrypterKeyDerivationStable(t *testing.T) {
	a := NewCrypter("same-secret")
	b := NewCrypter("same-secret")
	enc, _ := a.Encrypt([]byte("x"))
	dec, err := b.Decrypt(enc)
	if err != nil || string(dec) != "x" {
		t.Fatalf("same secret should decrypt: %v", err)
	}
}
