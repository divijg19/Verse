package linktoken

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func testKey(seed string) Key {
	digest := sha256.Sum256([]byte(seed))
	return Derive(digest[:])
}

func TestMintRecoverRoundTrip(t *testing.T) {
	key := testKey("secret-one")
	for _, kind := range []Kind{Private, Public} {
		seed, token, err := Mint(key, kind)
		if err != nil {
			t.Fatal(err)
		}
		if len(seed) != 64 || len(token) != 64 {
			t.Fatalf("seed/token length = %d/%d, want 64/64", len(seed), len(token))
		}
		if _, err := hex.DecodeString(token); err != nil {
			t.Fatalf("token is not lower-case hex: %v", err)
		}
		got, err := Recover(key, kind, seed, HashToken(token))
		if err != nil || got != token {
			t.Fatalf("recover = %q, %v; want %q", got, err, token)
		}
	}
}

func TestDomainsAreSeparated(t *testing.T) {
	key := testKey("secret-one")
	seed, token, err := Mint(key, Private)
	if err != nil {
		t.Fatal(err)
	}
	// The same seed under the public label yields a different token.
	if other := derive(key, Public, seed); other == token {
		t.Fatal("private and public derivation produced the same token")
	}
}

func TestRecoverFailsClosed(t *testing.T) {
	key := testKey("secret-one")
	seed, token, err := Mint(key, Private)
	if err != nil {
		t.Fatal(err)
	}
	hash := HashToken(token)

	if _, err := Recover(testKey("secret-two"), Private, seed, hash); err == nil {
		t.Error("a rotated key still recovered the token")
	}
	if _, err := Recover(key, Private, seed, hash[:len(hash)-1]+"0"); err == nil {
		t.Error("a hash mismatch still recovered the token")
	}
	if _, err := Recover(key, Private, "", hash); err == nil {
		t.Error("an empty seed still recovered the token")
	}
	if _, err := Recover(key, Private, "not-hex", hash); err == nil {
		t.Error("a malformed seed still recovered the token")
	}
}

// TestHashTokenHashesTheHexString pins the one detail that would otherwise break every resolved
// link: the stored hash is SHA-256 of the 64-character token string, not of its 32 raw bytes.
func TestHashTokenHashesTheHexString(t *testing.T) {
	token := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	sum := sha256.Sum256([]byte(token))
	want := hex.EncodeToString(sum[:])
	if got := HashToken(token); got != want {
		t.Fatalf("HashToken = %q, want %q", got, want)
	}
}
