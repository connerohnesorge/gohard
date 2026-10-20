package sema

import "testing"

// TestCaseCoverageFolded: choices with folded locally static bounds
// (INTEGER'RIGHT - 1) count for the coverage of a case statement, and
// every choice repeating a covered value is reported (LRM 10.9).
func TestCaseCoverageFolded(t *testing.T) {
	checkMarked(t, []markedCase{{name: "case", std: "1993", src: `
entity e is end;
architecture a of e is
begin
  process
    variable x : integer;
  begin
    case x is --E
      when integer'left to integer'right - 1 => null;
    end case;
    case x is
      when integer'left to integer'right - 1 => null;
      when integer'right => null;
    end case;
    case x is
      when 1 to 100 => null;
      when 50 => null; --E
      when 60 to 64 => null; --E
      when others => null;
    end case;
    wait;
  end process;
end;
`}})
}

// TestNamedSliceAggregate: a VHDL-2008 named association whose value
// is a slice gives the elements of its choice's range, so the
// aggregate's length counts them (LRM 9.3.3.3).
func TestNamedSliceAggregate(t *testing.T) {
	checkMarked(t, []markedCase{{name: "agg", std: "2008", src: `
package p is
  constant c6 : integer_vector(1 to 3) := (1, 2, 3);
  constant c10 : integer_vector(1 to 3) := (1 to 3 => c6);
  constant c11 : integer_vector(1 to 3) := (1 to 2 => c6(1 to 2)); --E
  constant c13 : integer_vector(1 to 3) := (1 to 2 => c6(1 to 2), 3 => 0);
end;
`}})
}

// TestCaseCoverageLoopParameter: a loop parameter's subtype is its
// range, so a case over it covers that range (LRM 10.9, 10.10).
func TestCaseCoverageLoopParameter(t *testing.T) {
	checkMarked(t, []markedCase{{name: "loop", std: "1993", src: `
entity e is end;
architecture a of e is
  signal foo : bit_vector(1 downto 0);
begin
  process
  begin
    for i in foo'range loop
      case i is
        when 0 => null;
        when 1 => null;
      end case;
      case i is --E
        when 0 => null;
      end case;
    end loop;
    for j in 0 to 2 loop
      case j is --E
        when 0 | 1 => null;
      end case;
    end loop;
    wait;
  end process;
end;
`}})
}
