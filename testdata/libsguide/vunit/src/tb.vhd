library vunit_lib;
context vunit_lib.vunit_context;

entity tb is
  generic (runner_cfg : string);
end entity tb;

architecture sim of tb is
begin
  main : process
  begin
    test_runner_setup(runner, runner_cfg);
    check_equal(1, 1, "one");
    info("done");
    test_runner_cleanup(runner);
  end process main;
end architecture sim;
