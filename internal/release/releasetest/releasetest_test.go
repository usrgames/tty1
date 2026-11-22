package releasetest

import (
	"errors"
	"testing"
	"time"

	"usr.games/tty1/internal/release"
)

// A stand-in that the real check refused, or that it accepted from anyone, would make every test
// built on it worthless. So the studio made here is held to the real check both ways.
func TestItsSignaturesAreGoodForItsOwnListAndForNoOther(t *testing.T) {
	studio, other := New(t), New(t)
	message := []byte(`{"schema": 1}`)
	sig := studio.Sign(message)
	if err := studio.Signers.Verify(message, sig, time.Now()); err != nil {
		t.Fatalf("wanted its own signature accepted, got %v", err)
	}
	if err := other.Signers.Verify(message, sig, time.Now()); !errors.Is(err, release.ErrSigner) {
		t.Errorf("another studio: wanted %v, got %v", release.ErrSigner, err)
	}
	if err := studio.Signers.Verify([]byte("another message"), sig, time.Now()); !errors.Is(err, release.ErrSignature) {
		t.Errorf("another message: wanted %v, got %v", release.ErrSignature, err)
	}
	if err := release.Studio().Verify(message, sig, time.Now()); !errors.Is(err, release.ErrSigner) {
		t.Errorf("the real studio's list: wanted %v, got %v", release.ErrSigner, err)
	}
}
