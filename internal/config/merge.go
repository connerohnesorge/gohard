package config

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/diag"
)

// ApplyDefines applies --define NAME=VALUE arguments in command-line
// order and returns the result. A define replaces the defines: entry
// with the same name (case-insensitive); each replaced gohard.yaml entry
// gives one log line for stderr (#20, #28). A later --define for the
// same name (compared case-insensitively) wins and is logged too, so a
// conflicting repeat is never silent. A malformed argument is cli/usage
// and a built-in identifier is config/builtin-define; both exit 2. c is
// not modified.
func ApplyDefines(c Config, args []string) (Config, []string,
	[]diag.Diagnostic,
) {
	var logs []string
	var ds []diag.Diagnostic
	defs := slices.Clone(c.Defines)
	given := map[int]string{} // index in defs -> the --define that set it
	for _, arg := range args {
		name, value, ok := strings.Cut(arg, "=")
		switch {
		case !ok:
			ds = append(ds, diag.New(diag.CLIUsage, "--define "+
				strconv.Quote(arg)+" must be NAME=VALUE"))
			continue
		case !IsIdentifier(name):
			ds = append(ds, diag.New(diag.CLIUsage, "--define "+
				strconv.Quote(arg)+": "+strconv.Quote(name)+
				" is not a VHDL identifier"))
			continue
		case IsBuiltin(name):
			ds = append(ds, diag.New(diag.ConfigBuiltinDefine, name))
			continue
		}
		i := slices.IndexFunc(defs, func(d Define) bool {
			return strings.EqualFold(d.Name, name)
		})
		if i < 0 {
			given[len(defs)] = arg
			defs = append(defs, Define{Name: name, Value: value})
			continue
		}
		if prev, ok := given[i]; ok {
			logs = append(logs, "--define "+arg+" overrides --define "+
				prev)
		} else if d := defs[i]; d.Line > 0 {
			logs = append(logs, "--define "+name+"="+value+
				" overrides defines."+d.Name+": "+
				strconv.Quote(d.Value)+" ("+FileName+":"+
				strconv.Itoa(d.Line)+":"+strconv.Itoa(d.Col)+")")
		}
		given[i] = arg
		defs[i] = Define{Name: name, Value: value}
	}
	slices.SortStableFunc(defs, func(a, b Define) int {
		return identLess(a.Name, b.Name)
	})
	c.Defines = defs
	return c, logs, diag.Normalize(ds)
}

// MergeEditor combines gohard.yaml with the editor's settings (decoded
// with Parse from the JSON the client sends). gohard.yaml wins: a key
// the file sets keeps the file's value, a key only the editor sets takes
// the editor's value, and every other key keeps its default. overridden
// lists, in table order, the keys both set; the server reports each in a
// window/logMessage (see OverrideMessage). Keys merge whole: a file
// that sets defines: replaces every editor define.
func MergeEditor(file, editor Config) (merged Config,
	overridden []string,
) {
	merged = file
	merged.set = maps.Clone(file.set)
	if merged.set == nil {
		merged.set = map[string]Pos{}
	}
	for _, k := range keys {
		switch {
		case file.IsSet(k.Name) && editor.IsSet(k.Name):
			overridden = append(overridden, k.Name)
		case editor.IsSet(k.Name):
			k.acc.copy(&merged, &editor)
			merged.set[k.Name] = editor.set[k.Name]
		}
	}
	return merged, overridden
}

// OverrideMessage is the window/logMessage text for an editor setting
// that gohard.yaml overrides.
func OverrideMessage(key string) string {
	return "gohard.yaml sets " + key + "; the editor setting " + key +
		" is ignored"
}
