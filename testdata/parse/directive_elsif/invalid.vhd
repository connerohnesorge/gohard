-- test: define=MODE=fast
`if MODE = "slow" then
package p is
end;
`elsif then
package q is
end;
`end if
