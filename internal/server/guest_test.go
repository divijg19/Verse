package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"
)

func TestAuthorAndGuestTokenDomainsDoNotCross(t *testing.T) {
	cfg := &authConfig{secret: []byte("0123456789abcdef0123456789abcdef")}

	guest, err := newGuestSession(
		"11111111-1111-4111-8111-111111111111",
		"22222222-2222-4222-8222-222222222222",
		time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	guestToken, err := cfg.guestToken(guest)
	if err != nil {
		t.Fatal(err)
	}

	// Same nonce and expiry, signed by the same secret, in the author's format. If the domain
	// prefixes ever collapse into one signing helper, one of these two parses must start working.
	author := &session{nonce: guest.nonce, expires: guest.expiresAt}
	authorToken := cfg.token(author)

	if _, err := cfg.parseToken(guestToken, time.Now()); err == nil {
		t.Error("a guest token was accepted as an author session token")
	}
	if _, err := cfg.parseGuestToken(authorToken, time.Now()); err == nil {
		t.Error("an author token was accepted as a guest session token")
	}

	// The cross-checks above are behavior; these pin the literal domains the behavior relies on.
	guestPayload, err := guestPayload(guest)
	if err != nil {
		t.Fatal(err)
	}
	if want := signForTest(cfg.secret, "guest|", guestPayload); !suffix(guestToken, want) {
		t.Error("the guest token is no longer signed over the guest| domain")
	}
	authorPayload := append(append([]byte(nil), author.nonce...), expiryBytesForTest(author.expires)...)
	if want := signForTest(cfg.secret, "", authorPayload); !suffix(authorToken, want) {
		t.Error("the author token is no longer signed over the bare session domain")
	}
}

func expiryBytesForTest(t time.Time) []byte {
	unix := t.Unix()
	out := make([]byte, 8)
	for i := range out {
		out[i] = byte((unix >> (56 - 8*i)) & 0xff)
	}
	return out
}

func signForTest(secret []byte, prefix string, payload []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(prefix))
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func suffix(value, part string) bool {
	return len(value) >= len(part) && value[len(value)-len(part):] == part
}
