package pow

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"testing"
	"time"
)

func mac(data []byte) []byte {
	h := hmac.New(sha256.New, []byte("key"))
	h.Write(data)
	return h.Sum(nil)
}

func TestRoundTrip(t *testing.T) {
	now := time.Now()
	c := New(mac, "site-1", 5000, time.Minute, now)
	n, ok := Solve(c)
	if !ok {
		t.Fatal("unsolvable")
	}
	sol := Solution{Challenge: c.Challenge, Salt: c.Salt, Signature: c.Signature, Number: n}
	if _, err := Verify(mac, sol, "site-1", now); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(mac, sol, "site-2", now); err == nil {
		t.Error("accepted for another site")
	}
	if _, err := Verify(mac, sol, "site-1", now.Add(2*time.Minute)); !errors.Is(err, ErrExpired) {
		t.Errorf("expired: %v", err)
	}
	wrong := sol
	wrong.Number++
	if _, err := Verify(mac, wrong, "site-1", now); err == nil {
		t.Error("wrong number accepted")
	}
	forged := sol
	forged.Salt = forged.Salt + "&x=1"
	if _, err := Verify(mac, forged, "site-1", now); err == nil {
		t.Error("changed salt accepted")
	}
	other := func(d []byte) []byte { h := hmac.New(sha256.New, []byte("other")); h.Write(d); return h.Sum(nil) }
	if _, err := Verify(other, sol, "site-1", now); err == nil {
		t.Error("signature from another key accepted")
	}
}
