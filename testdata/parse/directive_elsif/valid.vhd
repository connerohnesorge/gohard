-- test: define=MODE=fast kind=InactiveBranch
`if MODE = "slow" then
package slow_p is
end;
`elsif MODE = "fast" then
package p is
end;
`end if
