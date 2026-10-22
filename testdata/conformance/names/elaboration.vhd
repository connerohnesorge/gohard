-- Name attributes computed at elaboration (LRM 16.2.5, 14.4.2): a
-- package constant, an architecture constant per instance and a
-- signal's initial value; aliases
-- have their own names and extended identifiers are lowered (as NVC
-- 1.23); for generate iterations over an enumeration or characters
-- show the parameter's position.
package names_elab_pkg is
  signal ps : bit;
  constant pc : string := ps'path_name;
end package names_elab_pkg;

use work.names_elab_pkg.all;

entity names_elab_sub is
  generic (nm : string := "x");
end entity names_elab_sub;

architecture a of names_elab_sub is
  signal ss : bit;
  alias al is ss;
  signal \Ext Sig\ : bit;
  constant k : string := ss'instance_name;
  signal init : string(1 to 19) := ss'path_name;
begin
  process
  begin
    report "sub " & k & " " & nm & " " & pc & " " & init;
    report "alias " & al'path_name & " " & al'simple_name & " " &
      \Ext Sig\'path_name & " " & \Ext Sig\'simple_name;
    wait;
  end process;
end architecture a;

entity names_elab_tb is
end entity names_elab_tb;

architecture x of names_elab_tb is
  type color is (red, green, blue);
begin
  u : entity work.names_elab_sub generic map (nm => "given");

  e : for c in green to blue generate
    process
    begin
      report "enum " & c'path_name;
      wait;
    end process;
  end generate e;

  ch : for l in character range 'a' to 'b' generate
    lp : process
    begin
      report "char " & l'path_name & " " & lp'instance_name;
      wait;
    end process lp;
  end generate ch;
end architecture x;
