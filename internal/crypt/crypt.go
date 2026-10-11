// Package crypt holds the privacy conventions of docs/design/data-model.md
// section 2: email addresses encrypted with versioned keys and matched by
// HMAC, IP addresses kept only as an HMAC under a key derived per month, and
// the random and signed tokens the service hands out.
package crypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var (
	ErrNoKey     = errors.New("no key for this ciphertext's version")
	ErrCorrupt   = errors.New("ciphertext is corrupt")
	ErrBadToken  = errors.New("token is not valid")
	errShortKeys = errors.New("each key must be 32 bytes, base64 encoded")
)

// Keys are the service's secrets. The zero value is unusable.
type Keys struct {
	data    map[byte]cipher.AEAD
	current byte
	hmacKey []byte
	ipKey   []byte
}

// ParseDataKeys reads "version:base64key,..." where each key is 32 bytes.
// The highest version encrypts; every listed version decrypts.
func ParseDataKeys(s string) (map[byte][]byte, error) {
	out := map[byte][]byte{}
	for part := range strings.SplitSeq(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		v, k, ok := strings.Cut(part, ":")
		n, err := strconv.Atoi(v)
		if !ok || err != nil || n < 1 || n > 255 {
			return nil, fmt.Errorf("data key %q: want version:base64key with a version from 1 to 255", v)
		}
		key, err := decodeKey(k)
		if err != nil {
			return nil, fmt.Errorf("data key %d: %w", n, err)
		}
		if _, dup := out[byte(n)]; dup {
			return nil, fmt.Errorf("data key %d appears twice", n)
		}
		out[byte(n)] = key
	}
	if len(out) == 0 {
		return nil, errors.New("no data key")
	}
	return out, nil
}

// DecodeKey reads one base64 encoded 32-byte key.
func DecodeKey(s string) ([]byte, error) { return decodeKey(s) }

func decodeKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == 32 {
			return b, nil
		}
	}
	return nil, errShortKeys
}

// New builds Keys from parsed secrets.
func New(data map[byte][]byte, hmacKey, ipKey []byte) (*Keys, error) {
	if len(data) == 0 || len(hmacKey) != 32 || len(ipKey) != 32 {
		return nil, errShortKeys
	}
	k := &Keys{data: map[byte]cipher.AEAD{}, hmacKey: hmacKey, ipKey: ipKey}
	for v, key := range data {
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		k.data[v] = gcm
		k.current = max(k.current, v)
	}
	return k, nil
}

// Encrypt seals plain with the current key: version byte, nonce, ciphertext.
func (k *Keys) Encrypt(plain string) []byte {
	gcm := k.data[k.current]
	out := make([]byte, 1+gcm.NonceSize(), 1+gcm.NonceSize()+len(plain)+gcm.Overhead())
	out[0] = k.current
	_, _ = rand.Read(out[1:])
	return gcm.Seal(out, out[1:1+gcm.NonceSize()], []byte(plain), []byte{k.current})
}

// Decrypt opens what Encrypt sealed, with whichever key version it names.
func (k *Keys) Decrypt(sealed []byte) (string, error) {
	if len(sealed) < 1 {
		return "", ErrCorrupt
	}
	gcm, ok := k.data[sealed[0]]
	if !ok {
		return "", ErrNoKey
	}
	n := gcm.NonceSize()
	if len(sealed) < 1+n+gcm.Overhead() {
		return "", ErrCorrupt
	}
	plain, err := gcm.Open(nil, sealed[1:1+n], sealed[1+n:], sealed[:1])
	if err != nil {
		return "", ErrCorrupt
	}
	return string(plain), nil
}

// NormalizeEmail is the form every email HMAC is taken of.
func NormalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// EmailHMAC matches an address without storing it.
func (k *Keys) EmailHMAC(email string) []byte {
	return mac(k.hmacKey, "email:"+NormalizeEmail(email))
}

// IPHMAC hashes an address under the key of t's month, so hashes from
// different months cannot be linked.
func (k *Keys) IPHMAC(ip string, t time.Time) []byte {
	month := t.UTC().Format("2006-01")
	return mac(mac(k.ipKey, "ip-month:"+month), ip)
}

// IPHMACs returns the address's hash for t's month and the month before,
// which is how long an IP ban lasts (moderation.md section 4.1).
func (k *Keys) IPHMACs(ip string, t time.Time) [][]byte {
	t = t.UTC()
	prev := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	return [][]byte{k.IPHMAC(ip, t), k.IPHMAC(ip, prev)}
}

// Sign makes a token that carries payload and proves the service issued it
// for purpose. Used for mail links, which must keep working without state.
func (k *Keys) Sign(purpose, payload string) string {
	sig := mac(k.hmacKey, "sign:"+purpose+":"+payload)[:16]
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// Verify returns the payload of a token Sign made for purpose.
func (k *Keys) Verify(purpose, token string) (string, error) {
	p, s, ok := strings.Cut(token, ".")
	if !ok {
		return "", ErrBadToken
	}
	payload, err1 := base64.RawURLEncoding.DecodeString(p)
	sig, err2 := base64.RawURLEncoding.DecodeString(s)
	if err1 != nil || err2 != nil {
		return "", ErrBadToken
	}
	want := mac(k.hmacKey, "sign:"+purpose+":"+string(payload))[:16]
	if !hmac.Equal(sig, want) {
		return "", ErrBadToken
	}
	return string(payload), nil
}

// MAC is an HMAC under a key derived from the HMAC key for purpose, for
// values like proof of work challenges.
func (k *Keys) MAC(purpose string, data []byte) []byte {
	return mac(mac(k.hmacKey, "derive:"+purpose), string(data))
}

func mac(key []byte, msg string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(msg))
	return h.Sum(nil)
}

// Token returns 32 random bytes, base64url encoded: session tokens, binders,
// verifiers, states.
func Token() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// Hash is how tokens are stored: SHA-256 of the token's text.
func Hash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// Random returns n random bytes.
func Random(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}
