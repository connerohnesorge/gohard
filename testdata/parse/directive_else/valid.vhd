-- test: define=MODE=fast kind=InactiveBranch
`if MODE = "slow" then
package slow_p is
end;
`else
package fast_p is
end;
`end if
