package config

import (
	"bytes"
	"fmt"
	"testing"
)

// TestIdentity: two loaded configurations have the same Identity
// exactly when their values and the positions of the keys they set
// are the same, so an analysis cache keyed on it never replays a
// diagnostic that points at an old key position (#37).
func TestIdentity(t *testing.T) {
	type cfg struct {
		src string
		c   Config
	}
	var cs []cfg
	for _, pad := range []string{"", "\n", "# c\n\n", "   \n"} {
		for _, body := range []string{"", "libraries: {}\n",
			"standard: \"2008\"\n", "standard: \"2008\"\nlibraries: {}\n",
			"libraries: {}\nstandard: \"2008\"\n", "lenient: true\n"} {
			src := pad + body
			c, _ := Parse("gohard.yaml", []byte(src))
			cs = append(cs, cfg{src, c})
		}
	}
	same := func(a, b Config) bool {
		if fmt.Sprintf("%+v", a.withoutSet()) !=
			fmt.Sprintf("%+v", b.withoutSet()) {
			return false
		}
		ka, kb := a.SetKeys(), b.SetKeys()
		if len(ka) != len(kb) {
			return false
		}
		for i := range ka {
			if ka[i] != kb[i] || a.Position(ka[i]) != b.Position(kb[i]) {
				return false
			}
		}
		return true
	}
	for _, a := range cs {
		again, _ := Parse("gohard.yaml", []byte(a.src))
		if !bytes.Equal(a.c.Identity(), again.Identity()) {
			t.Errorf("%q: Identity differs between loads", a.src)
		}
		for _, b := range cs {
			eq := bytes.Equal(a.c.Identity(), b.c.Identity())
			if eq != same(a.c, b.c) {
				t.Errorf("%q vs %q: same identity %v, same config %v",
					a.src, b.src, eq, !eq)
			}
		}
	}
}

func (c Config) withoutSet() Config {
	c.set = nil
	return c
}
