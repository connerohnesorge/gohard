package stdlib

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/internal/diag"
)

// sumsFile is the integrity manifest: one "<sha256 hex>  <path>" line
// per bundled file (the format of sha256sum, so `sha256sum -c
// SHA256SUMS` checks a checkout), sorted by path.
const sumsFile = "SHA256SUMS"

// Sum is the expected hash of one bundled file.
type Sum struct {
	Path string
	Hex  string
}

// Sums returns the integrity manifest, sorted by path. Malformed lines
// are skipped; Verify reports the files they leave without a hash.
func Sums() []Sum {
	raw, err := sources.ReadFile(sumsFile)
	if err != nil {
		return nil
	}
	return parseSums(string(raw))
}

func parseSums(s string) []Sum {
	var out []Sum
	for line := range strings.Lines(s) {
		h, p, ok := strings.Cut(strings.TrimRight(line, "\r\n"), "  ")
		if !ok || len(h) != 2*sha256.Size || !validName(p) {
			continue
		}
		out = append(out, Sum{Path: p, Hex: h})
	}
	slices.SortFunc(out, func(a, b Sum) int {
		return strings.Compare(a.Path, b.Path)
	})
	return out
}

// hashed returns every path that must carry a hash: the VHDL files and
// the license files, sorted.
func hashed() []string {
	out := []string{"AUTHORS.md", "LICENSE"}
	for _, f := range manifest {
		out = append(out, f.Path)
	}
	slices.Sort(out)
	return out
}

// HashHex returns the lower-case hex SHA-256 of b.
func HashHex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

type tamperKey struct{}

// WithTamper returns a context under which Verify sees the bundled file
// at p with its first byte changed. It is the test hook of the exit-2
// fixture (#33, testdata/cli setup `tamper-stdlib`): only tests call
// it, and a context is the only way in, so no flag, file or environment
// variable can reach it.
func WithTamper(ctx context.Context, p string) context.Context {
	prev, _ := ctx.Value(tamperKey{}).([]string)
	return context.WithValue(ctx, tamperKey{},
		append(slices.Clip(prev), p))
}

// Verify checks every bundled file against its integrity hash (#20,
// #33). Each file that is missing, has no hash, or whose bytes differ
// gives a stdlib/integrity diagnostic, a tool failure (exit 2). The
// result is sorted and depends only on the embedded bytes and the
// tamper hook in ctx.
func Verify(ctx context.Context) []diag.Diagnostic {
	return verify(Sums(), func(p string) ([]byte, bool) {
		b, err := sources.ReadFile(p)
		if err != nil {
			return nil, false
		}
		if tampered(ctx, p) {
			if len(b) == 0 {
				b = []byte{0}
			} else {
				b[0] ^= 0x20 // ReadFile returned a private copy
			}
		}
		return b, true
	})
}

func tampered(ctx context.Context, p string) bool {
	if ctx == nil {
		return false
	}
	ps, _ := ctx.Value(tamperKey{}).([]string)
	return slices.Contains(ps, p)
}

func verify(sums []Sum, read func(string) ([]byte, bool),
) []diag.Diagnostic {
	want := map[string]string{}
	for _, s := range sums {
		want[s.Path] = s.Hex
	}
	var out []diag.Diagnostic
	for _, p := range hashed() {
		b, ok := read(p)
		h, listed := want[p]
		if !ok || !listed || HashHex(b) != h {
			out = append(out, diag.New(diag.StdlibIntegrity, p))
		}
	}
	return diag.Normalize(out)
}

// InternalError is the diagnostic for an error found inside the bundled
// library lib ("std" or "ieee"): a tool failure (exit 2), because the
// bundled text is fixed and checked (#20, #33). Analysis reports it
// instead of the error's own code.
func InternalError(lib, problem string) diag.Diagnostic {
	return diag.New(diag.StdlibInternal, lib, problem)
}
