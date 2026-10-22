package corpus

import (
	"strconv"
	"strings"
)

// Release is the milestone under test: the one switch every
// milestone-dependent check reads (a design's milestone, its
// expected_v1_0 outcome, the oracle goldens). It moves to v1.1 when
// the v1.1 work lands (#18, #27).
const Release = "v1.0"

// Reached reports whether milestone ("vMAJOR.MINOR") is at or before
// Release, so its designs run against their own expectations. An
// empty or malformed milestone is never reached.
func Reached(milestone string) bool {
	return reachedBy(milestone, Release)
}

// reachedBy reports whether milestone is at or before release.
func reachedBy(milestone, release string) bool {
	m, ok := parseMilestone(milestone)
	r, rok := parseMilestone(release)
	if !ok || !rok {
		return false
	}
	return m[0] < r[0] || m[0] == r[0] && m[1] <= r[1]
}

// parseMilestone reads "vMAJOR.MINOR" with decimal parts.
func parseMilestone(s string) ([2]int, bool) {
	rest, ok := strings.CutPrefix(s, "v")
	maj, minor, dot := strings.Cut(rest, ".")
	if !ok || !dot {
		return [2]int{}, false
	}
	a, err1 := strconv.Atoi(maj)
	b, err2 := strconv.Atoi(minor)
	if err1 != nil || err2 != nil || a < 0 || b < 0 ||
		strconv.Itoa(a) != maj || strconv.Itoa(b) != minor {
		return [2]int{}, false
	}
	return [2]int{a, b}, true
}
