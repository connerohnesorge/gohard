-- test: kind=none allow=syntax/directive-error
`if TOOL_TYPE = "SIMULATION" then
`error "not for simulation"
`end if
package p is
end;
