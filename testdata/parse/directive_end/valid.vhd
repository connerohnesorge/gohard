-- test: kind=none
`if TOOL_NAME = "gohard" then
package p is
end;
`end if
`if TOOL_NAME /= "other" then
package q is
end;
`end
