package env

import (
	"fmt"
	"strconv"
)

// SeedRandom is the --seed value that picks a seed through Rand.
const SeedRandom = "random"

// ParseSeed parses the value of `--seed` (#29): a decimal uint64, or
// "random", which draws a non-zero seed from r so the run really is
// shuffled. random reports whether the seed was drawn; the caller must
// then print it (see SeedLabel) so the run can be repeated.
//
// Only plain decimal digits are accepted: no sign, no spaces, no base
// prefix, so every seed has exactly one spelling.
func ParseSeed(s string, r Rand) (seed uint64, random bool, err error) {
	if s == SeedRandom {
		for seed == 0 {
			seed = r.Uint64()
		}
		return seed, true, nil
	}
	if s == "" || s[0] < '0' || s[0] > '9' {
		return 0, false, seedError(s)
	}
	seed, err = strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, false, seedError(s)
	}
	return seed, false, nil
}

func seedError(s string) error {
	return fmt.Errorf("invalid seed %q: want a whole number from 0 to "+
		"%d or %q", s, uint64(1<<64-1), SeedRandom)
}

// SeedLabel is how a seed is shown to the user, in the code lens and
// when `--seed random` prints its choice: "seed N".
func SeedLabel(seed uint64) string {
	return "seed " + strconv.FormatUint(seed, 10)
}
