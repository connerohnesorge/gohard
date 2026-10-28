entity top is
end entity top;

architecture rtl of top is
begin
end architecture rtl;

-- Bound to the instance u of tb, not to a design entity: GoHard binds
-- verification units to design entities only.
vunit checks(tb.u) {
  assert always true;
}
