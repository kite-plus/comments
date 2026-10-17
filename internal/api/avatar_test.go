package api

import (
	"strings"
	"testing"
)

func TestInitialOf(t *testing.T) {
	for in, want := range map[string]string{
		"林间": "林", " bob builder": "B", "Émile": "É", "7th": "7",
		"🌿 叶子": "", "": "", "<b>": "", "  ": "",
	} {
		if got := initialOf(in); got != want {
			t.Errorf("initialOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGeneratedAvatar(t *testing.T) {
	a := string(generatedAvatar("seed-1", "林"))
	if a != string(generatedAvatar("seed-1", "林")) {
		t.Fatal("the drawing is not stable for a seed")
	}
	if !strings.Contains(a, ">林</text>") || !strings.Contains(a, "Songti SC") {
		t.Errorf("a Han initial is not drawn in the serif: %s", a)
	}
	if b := string(generatedAvatar("seed-1", "B")); !strings.Contains(b, ">B</text>") || !strings.Contains(b, "Georgia") {
		t.Errorf("a Latin initial is not drawn in Georgia: %s", b)
	}
	if k := string(generatedAvatar("seed-1", "")); strings.Contains(k, "<text") || !strings.Contains(k, "<path") {
		t.Errorf("no initial should draw the mark: %s", k)
	}
	if got := generatedURL("abcd", "🌿 叶子"); got != "/avatars/generated/abcd.svg?c=" {
		t.Errorf("generatedURL = %q", got)
	}
	if got := generatedURL("abcd", "林间"); got != "/avatars/generated/abcd.svg?c=%E6%9E%97" {
		t.Errorf("generatedURL = %q", got)
	}
}
