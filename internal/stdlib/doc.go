// Package stdlib is the bundled VHDL std and ieee libraries (#33).
//
// The IEEE-P1076 packages at tag 1076-2019 (Apache-2.0, see LICENSE,
// AUTHORS.md and README.md for the pinned commit) are vendored
// unchanged under std/ and ieee/ and embedded in the binary, so nothing
// has to be installed to analyze, hover or simulate code that uses
// them.
//
//   - Files lists the VHDL files in analysis order and Source returns
//     their bytes; editors open them read-only, and hover and
//     go-to-definition land in this text.
//   - Verify checks every embedded file against SHA256SUMS. A mismatch
//     is stdlib/integrity, a tool failure (exit 2); an error found
//     inside the bundled text is reported with InternalError
//     (stdlib/internal-error, exit 2). WithTamper is the test hook that
//     makes a file fail.
//   - Decls lists every declaration the packages make, each tagged with
//     the first standard that has it (firststd.tsv). ViewOf(std) hides
//     the declarations newer than std: 2008 hides the 2019 additions,
//     1993 and 2002 hide the 2008 and 2019 additions. Every view
//     analyzes the same 2019 text; a view hides names, not text.
//   - Prebuilt is the hook for the build-time analysis (#34).
//
// Everything here is a pure function of the embedded bytes (P1).
package stdlib
