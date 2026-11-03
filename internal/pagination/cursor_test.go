package pagination

import (
	"testing"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

func TestCursorScopeAndSignature(t *testing.T) {
	signer, err := NewSigner([]byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	scope := Cursor{Kind: "comments", PostID: 4, ParentID: 2, Order: domain.SortOldest}
	token := signer.Encode(
		Cursor{Kind: scope.Kind, PostID: scope.PostID, ParentID: scope.ParentID, Order: scope.Order, LastID: 42},
	)
	if id, err := signer.Decode(token, scope); err != nil || id != 42 {
		t.Fatalf("roundtrip failed id=%d err=%v", id, err)
	}
	badScope := scope
	badScope.ParentID = 3
	if _, err := signer.Decode(token, badScope); err == nil {
		t.Fatal("foreign parent cursor accepted")
	}
	if _, err := signer.Decode(token+"x", scope); err == nil {
		t.Fatal("tampered cursor accepted")
	}
}
