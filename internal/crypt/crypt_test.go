package crypt

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

func key(b byte) string { return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32)) }

func testKeys(t *testing.T, data string) *Keys {
	t.Helper()
	d, err := ParseDataKeys(data)
	if err != nil {
		t.Fatal(err)
	}
	k, err := New(d, bytes.Repeat([]byte{9}, 32), bytes.Repeat([]byte{8}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestEncryptRotates(t *testing.T) {
	old := testKeys(t, "1:"+key(1))
	sealed := old.Encrypt("Reader@Example.com")
	if sealed[0] != 1 || bytes.Contains(sealed, []byte("Reader")) {
		t.Fatalf("sealed = %x", sealed)
	}
	both := testKeys(t, "1:"+key(1)+", 2:"+key(2))
	if got, err := both.Decrypt(sealed); err != nil || got != "Reader@Example.com" {
		t.Fatalf("decrypt with rotated keys = %q, %v", got, err)
	}
	if fresh := both.Encrypt("x"); fresh[0] != 2 {
		t.Errorf("new ciphertexts use version %d, want 2", fresh[0])
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := both.Decrypt(sealed); err == nil {
		t.Error("tampered ciphertext opened")
	}
	if _, err := testKeys(t, "3:"+key(3)).Decrypt(both.Encrypt("x")); !errors.Is(err, ErrNoKey) {
		t.Errorf("unknown version: %v", err)
	}
}

func TestParseDataKeysRejects(t *testing.T) {
	for _, bad := range []string{"", "x", "0:" + key(1), "1:short", "1:" + key(1) + ",1:" + key(2)} {
		if _, err := ParseDataKeys(bad); err == nil {
			t.Errorf("ParseDataKeys(%q) accepted", bad)
		}
	}
}

func TestHMACs(t *testing.T) {
	k := testKeys(t, "1:"+key(1))
	if !bytes.Equal(k.EmailHMAC(" A@B.example "), k.EmailHMAC("a@b.example")) {
		t.Error("email HMAC is not over the normalized address")
	}
	oct := time.Date(2026, 10, 31, 23, 0, 0, 0, time.UTC)
	nov := time.Date(2026, 11, 1, 1, 0, 0, 0, time.UTC)
	if bytes.Equal(k.IPHMAC("1.2.3.4", oct), k.IPHMAC("1.2.3.4", nov)) {
		t.Error("IP hashes link across months")
	}
	hs := k.IPHMACs("1.2.3.4", nov)
	if !bytes.Equal(hs[1], k.IPHMAC("1.2.3.4", oct)) {
		t.Error("previous month's hash missing")
	}
}

func TestSignedTokens(t *testing.T) {
	k := testKeys(t, "1:"+key(1))
	tok := k.Sign("unsubscribe", "u:42")
	if got, err := k.Verify("unsubscribe", tok); err != nil || got != "u:42" {
		t.Fatalf("Verify = %q, %v", got, err)
	}
	if _, err := k.Verify("mute", tok); err == nil {
		t.Error("token accepted for another purpose")
	}
	p, s, _ := strings.Cut(tok, ".")
	forged := base64.RawURLEncoding.EncodeToString([]byte("u:43")) + "." + s
	if _, err := k.Verify("unsubscribe", forged); err == nil || p == "" {
		t.Error("forged payload accepted")
	}
}
