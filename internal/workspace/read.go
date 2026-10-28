package workspace

import (
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/env"
)

// Source is a file and its bytes.
type Source struct {
	File
	Text []byte
}

// Read reads every file that is not TooLarge through fsys, running at
// most limit reads at once on jobs (limit <= 0 is the runner's default).
// It returns the sources in the order of files and a
// workspace/unreadable error for each file that could not be read. A
// file that grew over the size limit since discovery is dropped with
// workspace/file-too-large. The result does not depend on the runner:
// if reads panic, env.Jobs re-raises the panic of the first such file in
// files order.
func Read(jobs env.Jobs, limit int, fsys env.FS, files []File,
	maxSize int64,
) ([]Source, []diag.Diagnostic) {
	if maxSize <= 0 {
		maxSize = MaxFileSize
	}
	type read struct {
		text []byte
		err  error
	}
	got := env.Map(jobs, limit, files, func(f File) (r read) {
		if f.TooLarge {
			return read{}
		}
		r.text, r.err = fsys.ReadFile(f.Name)
		return r
	})
	var (
		srcs  []Source
		diags []diag.Diagnostic
	)
	for i, f := range files {
		r := got[i]
		switch {
		case f.TooLarge:
		case r.err != nil:
			diags = append(diags, Unreadable(f.Path, r.err))
		case int64(len(r.text)) > maxSize:
			diags = append(diags,
				TooLarge(f.Path, int64(len(r.text)), maxSize))
		default:
			srcs = append(srcs, Source{File: f, Text: r.text})
		}
	}
	return srcs, diags
}
