package sema

import (
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
)

// A Probe sees the phases in order and balanced overload resolutions,
// and never changes the result (#19).
func TestProbe(t *testing.T) {
	src := []byte(`library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;
entity e is end entity;
architecture a of e is
  constant c : unsigned(3 downto 0) := to_unsigned(3, 4) + 1;
  signal s : std_logic := '0' and '1';
begin
  s <= not s or undefined_name;
end architecture;
`)
	in := Input{Files: []Source{{Path: "e.vhd", Text: src}},
		Config: config.Defaults()}
	plain := Analyze(in)
	var mu sync.Mutex
	var phases []string
	starts, ends := 0, 0
	in.Probe = &Probe{
		Phase: func(n string) { phases = append(phases, n) },
		Overload: func() func() {
			mu.Lock()
			starts++
			mu.Unlock()
			return func() {
				mu.Lock()
				ends++
				mu.Unlock()
			}
		},
	}
	probed := Analyze(in)
	if !reflect.DeepEqual(plain.Diagnostics, probed.Diagnostics) {
		t.Error("the probe changed the diagnostics")
	}
	want := []string{"units", "cross-file", "static", "done"}
	if !slices.Equal(phases, want) {
		t.Errorf("phases %v, want %v", phases, want)
	}
	if starts == 0 || starts != ends {
		t.Errorf("overload starts %d, ends %d", starts, ends)
	}
	in.Probe = &Probe{} // nil hooks are allowed
	Analyze(in)
}
