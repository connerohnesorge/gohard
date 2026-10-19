-- prot_cond_tb: self-checking; one std.reflection check per type class
-- (scalar, array, record, access, protected), the std.env 2019
-- additions, 64-bit integers, conditional return, inferred constraints,
-- a generic map on a subprogram call and composite 'IMAGE.
use std.env.all;
use std.reflection.all;
use work.prot_cond_pkg.all;

entity prot_cond_tb is
end entity prot_cond_tb;

architecture sim of prot_cond_tb is
  type rec_t is record
    a : integer;
    b : bit;
  end record rec_t;
  type int_ptr is access integer;

  shared variable cnt : counter_t;
begin
  dut : entity work.prot_cond
    port map (c => cnt);

  check : process
    variable i   : integer := 42;
    variable arr : bit_vector(0 to 3) := "1010";
    variable rc  : rec_t := (a => 7, b => '1');
    variable p   : int_ptr := new integer'(9);
    variable vm  : value_mirror;
    -- Inferred constraint: the subtype comes from the initial value.
    variable inf : bit_vector := "10110";
    -- 64-bit integer.
    variable big : integer := 2**40;
  begin
    wait for 1 ns;

    -- Protected-type port: the DUT incremented the shared counter.
    assert cnt.get = 3 report "protected port: count /= 3"
      severity failure;

    -- std.reflection, one check per type class.
    vm := i'reflect;
    assert vm.get_value_class = CLASS_INTEGER
      report "reflect scalar: class" severity failure;
    assert vm.to_integer.value = 42
      report "reflect scalar: value" severity failure;
    vm := arr'reflect;
    assert vm.get_value_class = CLASS_ARRAY
      report "reflect array: class" severity failure;
    assert vm.to_array.get_subtype_mirror.length = 4
      report "reflect array: length" severity failure;
    vm := rc'reflect;
    assert vm.get_value_class = CLASS_RECORD
      report "reflect record: class" severity failure;
    assert vm.to_record.get(0).to_integer.value = 7
      report "reflect record: element 0" severity failure;
    vm := p'reflect;
    assert vm.get_value_class = CLASS_ACCESS
      report "reflect access: class" severity failure;
    assert not vm.to_access.is_null
      report "reflect access: null" severity failure;
    vm := cnt'reflect;
    assert vm.get_value_class = CLASS_PROTECTED
      report "reflect protected: class" severity failure;

    -- std.env 2019 additions.
    assert VHDL_VERSION = "2019"
      report "std.env.VHDL_VERSION" severity failure;
    assert TOOL_NAME = C_TOOL
      report "std.env.TOOL_NAME /= conditional TOOL_NAME" severity failure;
    assert GETENV("GOHARD_CORPUS_PROT_COND_UNSET") = ""
      report "std.env.GETENV of an unset name" severity failure;
    assert FILE_LINE > 0
      report "std.env.FILE_LINE" severity failure;

    -- 64-bit integers.
    assert big = 1099511627776 report "64-bit integer" severity failure;
    assert integer'high = 9223372036854775807
      report "integer'high is not 64-bit" severity failure;

    -- Conditional return and generic map on a subprogram call.
    assert clip(-5) = 0 and clip(3) = 3
      report "conditional return" severity failure;
    assert scale generic map (k => 3) (x => 4) = 12
      report "generic map on subprogram call" severity failure;

    -- Inferred constraint.
    assert inf'length = 5 report "inferred constraint" severity failure;

    -- Composite 'IMAGE.
    assert rec_t'image(rc) = "(7,'1')"
      report "record 'image" severity failure;
    assert bit_vector'image(arr) = """1010"""
      report "array 'image" severity failure;

    -- Assert counter API: nothing above failed, so the count is 0; one
    -- deliberate non-halting warning must then move it to exactly 1.
    assert GetVhdlAssertCount = 0
      report "assert counter before probe" severity failure;
    assert false report "prot_cond: counter probe" severity warning;
    assert GetVhdlAssertCount = 1 and GetVhdlAssertCount(WARNING) = 1
      report "assert counter after probe" severity failure;

    finish;
  end process check;
end architecture sim;
