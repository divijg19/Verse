package services

import "math/rand/v2"

// Prompt selection deliberately uses a non-cryptographic source: the value is a creative prompt
// with no security property, and Caelum gains nothing from unpredictability. gosec's G404 is
// triaged as not applicable here. Session and CSRF tokens use crypto/rand, where it matters.

// Prompts is a list of conceptual, poetic prompts (non-imperative).
var Prompts = []string{
	"a forgotten shoreline",
	"silence within a crowd",
	"the moon as witness",
	"a door left slightly open",
	"winter without snow",
	"dust in late sunlight",
	"a letter never sent",
	"footsteps fading into fog",
	"the weight of unsaid things",
	"lanterns across dark water",
}

// RandomPrompt returns a random prompt from the static pool.
func RandomPrompt() string {
	if len(Prompts) == 0 {
		return ""
	}
	// #nosec G404 -- a creative prompt has no security property. Predictability is irrelevant
	// here, and Caelum gains nothing from unpredictability. Anything that must resist guessing,
	// namely session nonces and CSRF tokens, uses crypto/rand.
	return Prompts[rand.IntN(len(Prompts))] // #nosec G404
}
