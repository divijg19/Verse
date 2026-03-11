package services

import (
	"math/rand"
	"time"
)

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

func init() {
	rand.New(rand.NewSource(time.Now().UnixNano()))
}

// RandomPrompt returns a random prompt from the static pool.
func RandomPrompt() string {
	if len(Prompts) == 0 {
		return ""
	}
	return Prompts[rand.Intn(len(Prompts))]
}
