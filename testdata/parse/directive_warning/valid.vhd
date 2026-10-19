-- test: kind=none allow=syntax/directive-warning
`if VHDL_VERSION < "2019" then
`warning "this design needs VHDL-2019"
`else
`warning "analysing with VHDL-2019"
`end if
package p is
end;
