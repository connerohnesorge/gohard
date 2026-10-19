-- test: define=PROFILE=wide kind=InactiveBranch
`if PROFILE = "wide" then
package wide_p is
end;
`elsif PROFILE = "narrow" or not (PROFILE /= "tiny") then
package narrow_p is
end;
`end if
