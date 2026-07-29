package collector

import (
	"path"
	"strings"
)

// match supports path.Match syntax plus a whole-segment ** wildcard.
func match(pattern, name string) (bool, error) {
	patternParts := strings.Split(strings.Trim(pattern, "/"), "/")
	nameParts := strings.Split(strings.Trim(name, "/"), "/")

	type state struct{ pattern, name int }
	memo := make(map[state]bool)
	seen := make(map[state]bool)
	var visit func(int, int) (bool, error)
	visit = func(patternIndex, nameIndex int) (bool, error) {
		key := state{patternIndex, nameIndex}
		if seen[key] {
			return memo[key], nil
		}
		seen[key] = true

		if patternIndex == len(patternParts) {
			memo[key] = nameIndex == len(nameParts)
			return memo[key], nil
		}
		if patternParts[patternIndex] == "**" {
			ok, err := visit(patternIndex+1, nameIndex)
			if err != nil || ok {
				memo[key] = ok
				return ok, err
			}
			if nameIndex < len(nameParts) {
				ok, err = visit(patternIndex, nameIndex+1)
				memo[key] = ok
				return ok, err
			}
			return false, nil
		}
		if nameIndex == len(nameParts) {
			return false, nil
		}
		ok, err := path.Match(patternParts[patternIndex], nameParts[nameIndex])
		if err != nil || !ok {
			return false, err
		}
		ok, err = visit(patternIndex+1, nameIndex+1)
		memo[key] = ok
		return ok, err
	}

	return visit(0, 0)
}
