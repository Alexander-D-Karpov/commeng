package domain

import "testing"

func TestTextLimits(t *testing.T) {
	tests := []struct {
		name, input string
		ok          bool
	}{
		{"empty", "  ", false},
		{"ascii", " hi ", true},
		{"2000 symbols", stringRunes('😀', 2000), true},
		{"2001 symbols", stringRunes('😀', 2001), false},
		{"nul", "abc\x00def", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Text(tt.input, 2000)
			if (err == nil) != tt.ok {
				t.Fatalf("Text(%q): error=%v, want success=%t", tt.name, err, tt.ok)
			}
		})
	}
}
func stringRunes(r rune, n int) string {
	x := make([]rune, n)
	for i := range x {
		x[i] = r
	}
	return string(x)
}
func TestIDs(t *testing.T) {
	for _, s := range []string{"0", "-1", "01", "1.0", "hi"} {
		if _, err := ParseID(s); err == nil {
			t.Errorf("expected invalid ID for %s", s)
		}
	}
	if id, err := ParseID("100"); err != nil || id != 100 {
		t.Errorf("expected 100, got %d, %v", id, err)
	}
}
