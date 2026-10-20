package libs

import (
	"context"
	"io/fs"
	"path"
	"path/filepath"
	"strings"

	"github.com/connerohnesorge/gohard/internal/env"
)

// Host is the machine the search looks at: its operating system and
// the doors to its files and environment. Paths of a Host ("host
// paths") are written the way that OS writes them: /opt/Xilinx on
// Linux and macOS, C:\Xilinx on Windows.
type Host struct {
	// OS is "linux", "darwin" or "windows" (runtime.GOOS values); any
	// other value is searched like Linux.
	OS string
	// FS reads the files; it is never written by the search.
	FS env.FS
	// Getenv reads PATH, the home folder and the vendor variables.
	Getenv env.Getenv
	// Root, when set, is an FS folder that stands for the file-system
	// root of the host: host path /opt/x is read at Root/opt/x and
	// C:\x at Root/C/x. Tests and testdata/cli fixtures use it to lay
	// out a Windows or macOS install on any machine; "" reads host
	// paths as they are.
	Root string
}

type hostKey struct{}

// WithHost returns a context whose `gohard libs detect` and
// "library not found" quick fix search h's OS and Root instead of the
// running machine (testdata/cli fixtures, #1). FS and Getenv still
// come from the run.
func WithHost(ctx context.Context, os, root string) context.Context {
	return context.WithValue(ctx, hostKey{}, Host{OS: os, Root: root})
}

// HostFrom returns the host of a run: OS and Root from WithHost when
// set, else goos and "".
func HostFrom(ctx context.Context, goos string, e env.Env) Host {
	h := Host{OS: goos}
	if v, ok := ctx.Value(hostKey{}).(Host); ok {
		h = v
	}
	h.FS, h.Getenv = e.FS, e.Getenv
	return h
}

func (h Host) windows() bool { return h.OS == "windows" }

// slash turns a host path into the slash form the search works in:
// C:\Xilinx becomes C:/Xilinx. Relative paths are returned as "".
func (h Host) slash(p string) string {
	if h.windows() {
		p = strings.ReplaceAll(p, `\`, "/")
		if len(p) >= 3 && isLetter(p[0]) && p[1] == ':' && p[2] == '/' {
			return strings.ToUpper(p[:1]) + ":" + path.Clean(p[2:])
		}
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		return ""
	}
	return path.Clean(p)
}

func isLetter(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

// Show writes a slash-form path the way the host OS writes it.
func (h Host) Show(p string) string {
	if h.windows() {
		return strings.ReplaceAll(p, "/", `\`)
	}
	return p
}

// fsPath is the FS name of a slash-form host path.
func (h Host) fsPath(p string) string {
	if h.Root == "" {
		return filepath.FromSlash(p)
	}
	rest := strings.TrimPrefix(p, "/")
	if h.windows() && len(p) >= 2 && p[1] == ':' {
		rest = p[:1] + p[2:]
	}
	return filepath.Join(h.Root, filepath.FromSlash(rest))
}

func (h Host) stat(p string) (fs.FileInfo, bool) {
	fi, err := h.FS.Stat(h.fsPath(p))
	return fi, err == nil
}

func (h Host) isDir(p string) bool {
	fi, ok := h.stat(p)
	return ok && fi.IsDir()
}

func (h Host) isFile(p string) bool {
	fi, ok := h.stat(p)
	return ok && fi.Mode().IsRegular()
}

// readDir lists a folder, sorted by name; nil when it cannot be read.
func (h Host) readDir(p string) []fs.DirEntry {
	es, err := h.FS.ReadDir(h.fsPath(p))
	if err != nil {
		return nil
	}
	return es
}

func (h Host) readFile(p string) ([]byte, bool) {
	b, err := h.FS.ReadFile(h.fsPath(p))
	return b, err == nil
}

func (h Host) getenv(k string) string {
	if h.Getenv == nil {
		return ""
	}
	v, _ := h.Getenv(k)
	return v
}

// home is the user's home folder in slash form, or "".
func (h Host) home() string {
	if h.windows() {
		return h.slash(h.getenv("USERPROFILE"))
	}
	return h.slash(h.getenv("HOME"))
}

// pathDirs are the folders of PATH in slash form, in order.
func (h Host) pathDirs() []string {
	sep := ":"
	if h.windows() {
		sep = ";"
	}
	var out []string
	for _, d := range strings.Split(h.getenv("PATH"), sep) {
		if s := h.slash(strings.Trim(d, `"`)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// lookPath finds program on PATH (with .exe, .bat or .cmd on
// Windows) and returns its slash-form path.
func (h Host) lookPath(program string) (string, bool) {
	names := []string{program}
	if h.windows() {
		names = []string{program + ".exe", program + ".bat",
			program + ".cmd"}
	}
	for _, d := range h.pathDirs() {
		for _, n := range names {
			if p := path.Join(d, n); h.isFile(p) {
				return p, true
			}
		}
	}
	return "", false
}

// up returns p with n final elements removed.
func up(p string, n int) string {
	for range n {
		p = path.Dir(p)
	}
	return p
}
