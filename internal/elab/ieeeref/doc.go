// Package ieeeref holds the reference checks of the IEEE package
// bodies GoHard interprets from source (#33, #35): every numeric_std
// arithmetic, comparison, shift, resize and conversion function, run
// through the shared evaluator (internal/sema's interpreter on
// internal/eval) on generated operands, against math/big. It has no
// API: the checks are its tests.
package ieeeref
