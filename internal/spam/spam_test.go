package spam

import (
	"testing"
	"time"
)

func TestCleanName(t *testing.T) {
	cases := map[string]string{
		"  Alice  ":       "Alice",
		"Al\u200bice":     "Alice",
		"\u202eecilA":     "ecilA",
		"a \t\n b":        "a b",
		"Bob\u2066\u2069": "Bob",
		"\ufeff读者\u200d":  "读者",
	}
	for in, want := range cases {
		if got := CleanName(in); got != want {
			t.Errorf("CleanName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHostsAndDomains(t *testing.T) {
	hosts := Hosts(`<a href="https://Shop.Spam.example/x?a=1&amp;b=2">x</a> <a href="mailto:a@b.example">m</a>`)
	if len(hosts) != 1 || hosts[0] != "shop.spam.example" {
		t.Fatalf("hosts = %v", hosts)
	}
	listed := map[string]bool{"spam.example": true}
	if !DomainListed("shop.spam.example", listed) || !DomainListed("spam.example.", listed) || DomainListed("notspam.example", listed) {
		t.Error("domain matching")
	}
}

func TestBlockedWord(t *testing.T) {
	if w, ok := BlockedWord("Buy CHEAP pills", []string{" ", "cheap"}); !ok || w != "cheap" {
		t.Errorf("BlockedWord = %q, %v", w, ok)
	}
	if _, ok := BlockedWord("fine", []string{"bad"}); ok {
		t.Error("false positive")
	}
	if got := NormalizeWords([]string{" a", "A", "", "b"}); len(got) != 2 {
		t.Errorf("NormalizeWords = %v", got)
	}
}

func TestNewAccount(t *testing.T) {
	now := time.Now()
	if !NewAccount(now.Add(-time.Hour), now) || NewAccount(now.Add(-25*time.Hour), now) {
		t.Error("new account boundary")
	}
}
