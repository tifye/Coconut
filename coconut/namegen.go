package coconut

import (
	"fmt"
	"math/rand"
	"strings"
)

var firstName bool = true

var prefixes = []string{"galactic", "shadow", "mystic", "star", "crystal", "void", "cyber", "draconian", "lunar", "nebula"}
var middles = []string{"knight", "wanderer", "archer", "sage", "rogue", "titan", "raider", "guardian", "hunter", "seer"}
var suffixes = []string{"blade", "sphere", "forge", "haven", "spire", "sanctum", "quest", "citadel", "rune", "portal"}

// Function to generate a random element from a list
func randomElement(list []string) string {
	return list[rand.Intn(len(list))]
}

// Function to generate a 3-part subdomain
func generateSubdomain() string {
	if firstName {
		firstName = false
		return "star-sage-sanctum"
	}
	prefix := randomElement(prefixes)
	middle := randomElement(middles)
	suffix := randomElement(suffixes)

	return strings.ToLower(fmt.Sprintf("%s-%s-%s", prefix, middle, suffix))
}
