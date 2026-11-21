// Package linktoken derives recoverable bearer tokens for authoring links.
//
// The raw token is never stored. Instead each row keeps a random, non-secret seed; the token is
// recomputed from that seed and a server-held key whenever the author loads Share. The existing
// token_hash stays the authoritative lookup value and doubles as the integrity check: a token is
// only handed out when sha256(derived) equals the stored hash. A database read alone therefore
// yields nothing usable, while a database read together with the application secret can recover
// active links -- the explicit trade this release makes for a Copy button that survives reload.
package linktoken

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// Kind separates the two capabilities so a seed cannot be used across surfaces.
type Kind string

const (
	Private Kind = "private-link-token-v1|"
	Public  Kind = "public-link-token-v1|"
)

// keyLabel domain-separates link derivation from session, CSRF, guest and rate-limit HMACs, all
// of which use the same VERSE_AUTH_SECRET-derived key in internal/server.
const keyLabel = "verse/linktoken/v1"

// Key is the domain-separated HMAC key used to derive link tokens.
type Key [32]byte

// Derive produces the link key from the server's already-hashed authentication secret. The result
// is deterministic across restarts while VERSE_AUTH_SECRET is unchanged, and changes with it.
func Derive(secret []byte) Key {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(keyLabel))
	var key Key
	copy(key[:], mac.Sum(nil))
	return key
}

// Mint creates a fresh random seed and the token derived from it. Both are lowercase hex so the
// token keeps the 64-character shape the resolve path and log redaction already expect.
func Mint(key Key, kind Kind) (seed, token string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate a link token seed: %w", err)
	}
	seed = hex.EncodeToString(raw)
	token = derive(key, kind, seed)
	return seed, token, nil
}

// Recover derives a token from a stored seed and returns it only when its hash matches wantHash.
// A NULL or malformed seed, or a key rotated since minting, fails closed with an error.
func Recover(key Key, kind Kind, seed, wantHash string) (string, error) {
	if seed == "" || wantHash == "" {
		return "", errors.New("link has no recoverable seed")
	}
	if _, err := hex.DecodeString(seed); err != nil || len(seed) != 64 {
		return "", errors.New("link has a malformed seed")
	}
	token := derive(key, kind, seed)
	if HashToken(token) != wantHash {
		return "", errors.New("link token does not match its stored hash")
	}
	return token, nil
}

// HashToken is the exact hash the resolve path stores and compares: SHA-256 of the hex string.
// Hashing raw bytes instead would make every derived token fail to resolve.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func derive(key Key, kind Kind, seed string) string {
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write([]byte(kind))
	_, _ = mac.Write([]byte(seed))
	return hex.EncodeToString(mac.Sum(nil))
}

// keyContextKey carries the derived key from the server layer to handlers. It lives here, in the
// leaf package, so both handlers and services can read it without an import cycle.
type keyContextKey struct{}

// WithKey attaches the derived link key to a request context.
func WithKey(ctx context.Context, key Key) context.Context {
	return context.WithValue(ctx, keyContextKey{}, key)
}

// KeyFrom returns the key injected by the server router.
func KeyFrom(ctx context.Context) (Key, bool) {
	key, ok := ctx.Value(keyContextKey{}).(Key)
	return key, ok
}
