package domain

import (
	"strings"
	"testing"
)

func TestTextRejectsControlCharacters(t *testing.T) {
	for _, input := range []string{"Alice\u0000Test", "Alice\nTest", "Alice\tTest", "Alice\rTest"} {
		if _, err := LabelText(input, 80); !HasCode(err, ErrInput) {
			t.Fatalf("input %q accepted; error %v", input, err)
		}
	}
}

func TestFieldAddsContextWithoutLosingCode(t *testing.T) {
	err := Field("title", Fault(ErrInput, "too long"))
	if !HasCode(err, ErrInput) || !strings.Contains(err.Error(), "title: ") {
		t.Fatalf("unqualified error: %v", err)
	}
}

func TestBodyAcceptsMultilineText(t *testing.T) {
	text, err := Text("First line\nSecond line\tpart", 2000)
	if err != nil || !strings.Contains(text, "\n") {
		t.Fatalf("multiline body rejected: text=%q err=%v", text, err)
	}
}
