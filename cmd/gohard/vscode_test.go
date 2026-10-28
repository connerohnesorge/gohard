package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/ide"
)

// lspTokenTypes and lspTokenModifiers are the semantic token types and
// modifiers LSP 3.17 defines; VS Code knows them without a
// contribution.
var (
	lspTokenTypes = []string{"namespace", "type", "class", "enum",
		"interface", "struct", "typeParameter", "parameter", "variable",
		"property", "enumMember", "event", "function", "method", "macro",
		"keyword", "modifier", "comment", "string", "number", "regexp",
		"operator", "decorator"}
	lspTokenModifiers = []string{"declaration", "definition", "readonly",
		"static", "deprecated", "abstract", "async", "modification",
		"documentation", "defaultLibrary"}
)

// vscodeManifest is the part of editors/vscode/package.json the test
// reads.
type vscodeManifest struct {
	Version     string `json:"version"`
	Contributes struct {
		Languages []struct {
			ID         string   `json:"id"`
			Extensions []string `json:"extensions"`
		} `json:"languages"`
		SemanticTokenTypes []struct {
			ID        string `json:"id"`
			SuperType string `json:"superType"`
		} `json:"semanticTokenTypes"`
		SemanticTokenModifiers []struct {
			ID string `json:"id"`
		} `json:"semanticTokenModifiers"`
		Configuration struct {
			Properties map[string]struct {
				Type                string `json:"type"`
				Default             any    `json:"default"`
				MarkdownDescription string `json:"markdownDescription"`
			} `json:"properties"`
		} `json:"configuration"`
	} `json:"contributes"`
}

// TestVSCodeManifest checks that the VS Code extension (#3) agrees with
// the server: it contributes every semantic token type and modifier of
// the legend that LSP does not define (and no other), each forwarded
// setting is a gohard.yaml key with the key table's type, default and
// text, every inlay_hints key is forwarded (#56), and the extension's
// version is the flake's, which release.yml checks against the tag.
func TestVSCodeManifest(t *testing.T) {
	ext := filepath.Join("..", "..", "editors", "vscode")
	raw, err := os.ReadFile(filepath.Join(ext, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m vscodeManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	c := m.Contributes

	if len(c.Languages) != 1 || c.Languages[0].ID != "vhdl" ||
		!slices.Equal(c.Languages[0].Extensions, []string{".vhd", ".vhdl"}) {
		t.Errorf("languages = %+v, want vhdl with .vhd and .vhdl",
			c.Languages)
	}

	var types, wantTypes []string
	for _, tt := range c.SemanticTokenTypes {
		types = append(types, tt.ID)
		if !slices.Contains(lspTokenTypes, tt.SuperType) {
			t.Errorf("token type %s: superType %q is not an LSP type",
				tt.ID, tt.SuperType)
		}
	}
	for _, tt := range ide.TokenTypes {
		if !slices.Contains(lspTokenTypes, tt) {
			wantTypes = append(wantTypes, tt)
		}
	}
	if !slices.Equal(types, wantTypes) {
		t.Errorf("semanticTokenTypes = %v, want %v", types, wantTypes)
	}
	var mods, wantMods []string
	for _, tm := range c.SemanticTokenModifiers {
		mods = append(mods, tm.ID)
	}
	for _, tm := range ide.TokenModifiers {
		if !slices.Contains(lspTokenModifiers, tm) {
			wantMods = append(wantMods, tm)
		}
	}
	if !slices.Equal(mods, wantMods) {
		t.Errorf("semanticTokenModifiers = %v, want %v", mods, wantMods)
	}

	var forwarded []string
	for name, p := range c.Configuration.Properties {
		if name == "gohard.path" || name == "gohard.trace.server" {
			continue
		}
		key, ok := strings.CutPrefix(name, "gohard.")
		k, known := config.LookupKey(key)
		if !ok || !known {
			t.Errorf("setting %s is not a gohard.yaml key", name)
			continue
		}
		forwarded = append(forwarded, key)
		if k.Type != config.TypeBool || p.Type != "boolean" {
			t.Errorf("setting %s: type %q, the key is not a bool or "+
				"the setting not a boolean", name, p.Type)
			continue
		}
		if fmt.Sprint(p.Default) != k.Default {
			t.Errorf("setting %s: default %v, key default %s", name,
				p.Default, k.Default)
		}
		if p.MarkdownDescription != k.Doc {
			t.Errorf("setting %s: markdownDescription\n%q\nwant the "+
				"key's doc\n%q", name, p.MarkdownDescription, k.Doc)
		}
	}
	var hints []string
	for _, k := range config.Keys() {
		if strings.HasPrefix(k.Name, "inlay_hints.") {
			hints = append(hints, k.Name)
		}
	}
	slices.Sort(forwarded)
	if !slices.Equal(forwarded, hints) {
		t.Errorf("forwarded settings %v, want the inlay_hints keys %v",
			forwarded, hints)
	}

	flake, err := os.ReadFile(filepath.Join("..", "..", "flake.nix"))
	if err != nil {
		t.Fatal(err)
	}
	fv := regexp.MustCompile(`(?m)^\s*version = "([^"]+)";$`).
		FindSubmatch(flake)
	if fv == nil {
		t.Fatal("flake.nix has no version line")
	}
	if m.Version != string(fv[1]) {
		t.Errorf("editors/vscode/package.json version %q, flake.nix %q",
			m.Version, fv[1])
	}
	rawLock, err := os.ReadFile(filepath.Join(ext, "package-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Version  string `json:"version"`
		Packages map[string]struct {
			Version string `json:"version"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(rawLock, &lock); err != nil {
		t.Fatal(err)
	}
	if lock.Version != m.Version || lock.Packages[""].Version != m.Version {
		t.Errorf("package-lock.json versions %q and %q, package.json %q",
			lock.Version, lock.Packages[""].Version, m.Version)
	}
}
