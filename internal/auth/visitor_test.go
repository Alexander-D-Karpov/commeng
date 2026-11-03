package auth

import "testing"

func TestVisitorSigned(t *testing.T) {
	issuer, err := NewVisitors([]byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	token, err := issuer.Issue()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := issuer.Verify(token); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	if _, err := issuer.Verify(token + "x"); err == nil {
		t.Fatal("tampered token accepted")
	}
}
