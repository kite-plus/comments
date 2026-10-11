// Package pow is the guests' proof of work (docs/design/moderation.md
// section 3.2), after the ALTCHA protocol: the server hides a random
// number in a SHA-256 challenge and signs it; the client finds the number
// by trying each in turn. The signature makes the check stateless; the
// caller records spent challenges so a solution counts once.
package pow

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math/big"
	"net/url"
	"strconv"
	"time"
)

// Algorithm is the only one offered.
const Algorithm = "SHA-256"

var (
	ErrInvalid = errors.New("proof of work is not valid")
	ErrExpired = errors.New("proof of work expired")
)

// MAC signs data with a key the caller holds.
type MAC func(data []byte) []byte

// Challenge is what the client solves.
type Challenge struct {
	Algorithm string `json:"algorithm"`
	Challenge string `json:"challenge"`
	MaxNumber int    `json:"maxnumber"`
	Salt      string `json:"salt"`
	Signature string `json:"signature"`
}

// Solution is what the client sends back.
type Solution struct {
	Challenge string `json:"challenge"`
	Salt      string `json:"salt"`
	Signature string `json:"signature"`
	Number    int    `json:"number"`
}

// New makes a challenge for a site, valid for ttl, hiding a number up to
// maxNumber.
func New(mac MAC, site string, maxNumber int, ttl time.Duration, now time.Time) Challenge {
	nonce := make([]byte, 12)
	_, _ = rand.Read(nonce)
	params := url.Values{"expires": {strconv.FormatInt(now.Add(ttl).Unix(), 10)}, "site": {site}}
	salt := hex.EncodeToString(nonce) + "?" + params.Encode()
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(maxNumber)+1))
	ch := hash(salt, int(n.Int64()))
	return Challenge{Algorithm: Algorithm, Challenge: ch, MaxNumber: maxNumber, Salt: salt, Signature: hex.EncodeToString(mac([]byte(ch)))}
}

// Verify checks a solution for site and returns when its challenge
// expires, which is how long the caller must remember it as spent.
func Verify(mac MAC, s Solution, site string, now time.Time) (time.Time, error) {
	sig, err := hex.DecodeString(s.Signature)
	if err != nil || !hmac.Equal(sig, mac([]byte(s.Challenge))) {
		return time.Time{}, ErrInvalid
	}
	_, query, ok := cut(s.Salt, '?')
	params, err := url.ParseQuery(query)
	if !ok || err != nil || params.Get("site") != site {
		return time.Time{}, ErrInvalid
	}
	exp, err := strconv.ParseInt(params.Get("expires"), 10, 64)
	if err != nil {
		return time.Time{}, ErrInvalid
	}
	expires := time.Unix(exp, 0)
	if now.After(expires) {
		return time.Time{}, ErrExpired
	}
	if s.Number < 0 || hash(s.Salt, s.Number) != s.Challenge {
		return time.Time{}, ErrInvalid
	}
	return expires, nil
}

// Solve finds the number, for tests.
func Solve(c Challenge) (int, bool) {
	for n := 0; n <= c.MaxNumber; n++ {
		if hash(c.Salt, n) == c.Challenge {
			return n, true
		}
	}
	return 0, false
}

// ID is the key a spent challenge is remembered by.
func ID(challenge string) []byte {
	b, err := hex.DecodeString(challenge)
	if err != nil {
		sum := sha256.Sum256([]byte(challenge))
		return sum[:]
	}
	return b
}

func hash(salt string, n int) string {
	sum := sha256.Sum256([]byte(salt + strconv.Itoa(n)))
	return hex.EncodeToString(sum[:])
}

func cut(s string, sep byte) (string, string, bool) {
	for i := range len(s) {
		if s[i] == sep {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}
