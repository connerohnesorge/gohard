// Package sema is GoHard's semantic analysis (#34): libraries and
// design units, declarative regions and visibility (LRM 12), the type
// representation, name resolution, expression typing, overload
// resolution with editor-mode candidates, static evaluation through
// the shared evaluator (internal/eval) and opaque-unit handling. The
// language server, `gohard check` and the simulator share it, so the
// editor never guesses.
//
// # Entry point
//
// Analyze(Input) -> *Result is a pure function of the snapshot (P1):
// the files (any order; paths unique), the loaded configuration and the
// tool version. Results never depend on file order, map order or the
// job runner (TestPermutation, TestRunners). The pipeline:
//
//  1. parse every file (a pure job per file on Input.Jobs; trees the
//     editor already has may be passed in Source.Parse);
//  2. scan each file's design units and their syntactic library-unit
//     references (scan.go);
//  3. index units by library (library.go): gohard.yaml folders map
//     files to libraries, the longest folder wins, the rest is work;
//     std and ieee come from the bundle;
//  4. order units by dependency level (Tarjan SCCs; a cycle is one
//     level whose units cannot see each other) and analyze each level's
//     units as parallel pure jobs; a unit job only reads units of lower
//     levels and the bundle, which are finished and never written again;
//  5. resolve deferred architecture references, report library
//     problems, mark opaque and unchecked units, run unit-level static
//     checks; merge diagnostics in canonical order.
//
// The bundled std/ieee packages (#33) are analyzed once per process as
// VHDL-2019 text (Bundle) and shared read-only. Per-standard views are
// applied at lookup through Decl.First: a project unit under 1993 does
// not see UNRESOLVED_UNSIGNED, fixed_pkg or std_logic_textio (all
// VHDL-2008), while UNSIGNED and its operations stay visible (a view
// hides names, not text). Errors inside the bundle are reported as
// stdlib/internal-error (exit 2); TestBundleClean keeps the bundle
// clean.
//
// # The resolved model
//
// Every name occurrence sema resolves is a Ref (file, name span, the
// Decl it denotes, the editor-mode Overload of a call or operator).
// Every Decl has its defining file and name span. Result.RefAt,
// Result.DeclAt and Result.References answer definition, references and
// rename across the workspace; internal/resolve builds the language
// server queries on them. Unit.Exprs (typed expressions), Unit.Body
// (the concurrent statement tree) and the sequential statement trees
// of processes (Stmt.Seq) and subprogram bodies (Decl.Seq, seq.go) are
// the typed-AST view elaboration and the simulator consume; they are
// internal and grow with #35.
//
// # Files and owners
//
// The core (this stage) is model.go, ident.go, typerep.go, expr.go,
// refs.go, body.go, scope.go, analyze.go, scan.go, library.go,
// bundle.go, unit.go, decls.go, typedecl.go, stmts.go, predef.go,
// syntax.go, seq.go and query.go. Each of these builder files holds a working
// first version and is the extension point of one area:
//
//   - names.go: name resolution (resolveName and its cases; predefined
//     attributes; selected names into libraries, packages, records,
//     protected types; call versus index of a parameterless call;
//     package pathnames of external names).
//   - modeview.go: VHDL-2019 mode views (#17): View on view
//     declarations, their aliases and view ports; 'CONVERSE; the
//     checks of view declarations, of assignments to view ports and
//     of their port-map actuals.
//   - types.go: expression typing (operand/expr/settle, literals,
//     aggregates, ranges, conditions, expect for sema/type-mismatch).
//   - typecheck.go: the typing rules types.go, typedecl.go and
//     overload.go hook in (#34): literal and aggregate contexts,
//     conversion operands, range and constraint types, case
//     expressions, record others, protected types and their methods,
//     VHDL-93 use-visible homographs.
//   - overload.go: overload resolution (fit, match, choose, finishCall;
//     the Overload result with candidates and reasons; Signature).
//   - libs.go: library diagnostics (library/unmapped with quick-fix
//     data, library/missing-unit, library/duplicate-unit,
//     library/import-cycle; deferred architecture references).
//   - static.go and interp.go: static evaluation (Evaluator: lowering
//     to eval IR, layouts, bindings per instance, the caches shared by
//     the instances of a design; pure function and procedure bodies
//     interpreted from source, ieee included; unit-level checks of
//     constants and signals). Per-instance checks live in
//     internal/elab/hier.
//   - unsupported.go: opaque units (encrypted IP, allow_opaque,
//     stand-in models, unchecked dependents, the constructs table for
//     unsupported/construct).
//
// Invariants (tests): results are independent of file order and job
// runner; incremental analysis equals a fresh one; sema never panics on
// any parser tree (TestNoPanic); every name in the clean oracle corpus
// resolves (TestCorpusRefs); the implicit operations of std.standard
// match the declarations its text lists (TestStdStandardComments).
package sema
