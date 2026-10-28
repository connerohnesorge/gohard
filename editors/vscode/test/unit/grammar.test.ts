// Tests of the TextMate grammar (syntaxes/vhdl.tmLanguage.json, #3)
// with the TextMate engine VS Code uses (vscode-textmate and its
// Oniguruma build).
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { before, test } from 'node:test';
import * as oniguruma from 'vscode-oniguruma';
import * as vsctm from 'vscode-textmate';

// out/test/unit -> editors/vscode
const extDir = join(__dirname, '..', '..', '..');
const repoDir = join(extDir, '..', '..');

let grammar: vsctm.IGrammar;

before(async () => {
  const wasm = readFileSync(
    join(extDir, 'node_modules', 'vscode-oniguruma', 'release', 'onig.wasm'),
  );
  await oniguruma.loadWASM(
    wasm.buffer.slice(wasm.byteOffset, wasm.byteOffset + wasm.byteLength),
  );
  const path = join(extDir, 'syntaxes', 'vhdl.tmLanguage.json');
  const registry = new vsctm.Registry({
    onigLib: Promise.resolve({
      createOnigScanner: (s: string[]) => new oniguruma.OnigScanner(s),
      createOnigString: (s: string) => new oniguruma.OnigString(s),
    }),
    loadGrammar: async (scope: string) =>
      scope === 'source.vhdl'
        ? vsctm.parseRawGrammar(readFileSync(path, 'utf8'), path)
        : null,
  });
  const g = await registry.loadGrammar('source.vhdl');
  assert.ok(g, 'grammar source.vhdl loads');
  grammar = g;
});

interface Token {
  start: number;
  end: number;
  scope: string; // the scopes below source.vhdl; "" for plain text
}

function tokenize(src: string): Token[][] {
  const lines: Token[][] = [];
  let stack = vsctm.INITIAL;
  for (const line of src.split('\n')) {
    const r = grammar.tokenizeLine(line, stack);
    lines.push(
      r.tokens.map((t) => ({
        start: t.startIndex,
        end: t.endIndex,
        scope: t.scopes.slice(1).join(' '),
      })),
    );
    stack = r.ruleStack;
  }
  return lines;
}

// tokenAt returns the token holding the first character of the first
// fragment on the given line of src, and the fragment's offset.
function tokenAt(src: string, fragment: string, line: number) {
  const at = src.split('\n')[line].indexOf(fragment);
  assert.ok(at >= 0, `${JSON.stringify(fragment)} not in ${src}`);
  const t = tokenize(src)[line].find((t) => t.start <= at && at < t.end);
  assert.ok(t, `no token at ${at} in ${src}`);
  return { t, at };
}

// scopeOf returns the scopes at the start of fragment in src.
function scopeOf(src: string, fragment: string, line = 0): string {
  return tokenAt(src, fragment, line).t.scope;
}

// whole asserts that fragment is exactly one token of src and one of
// its scopes starts with scope.
function whole(src: string, fragment: string, scope: string, line = 0) {
  const { t, at } = tokenAt(src, fragment, line);
  const what = `${JSON.stringify(fragment)} in ${JSON.stringify(src)}`;
  assert.deepEqual([t.start, t.end], [at, at + fragment.length], what);
  assert.ok(
    t.scope.split(' ').some((s) => s.startsWith(scope)),
    `${what}: ${t.scope}, want ${scope}`,
  );
}

function keywordsByClass(): Map<string, string[]> {
  const yaml = readFileSync(
    join(repoDir, 'internal', 'token', 'keywords.yaml'),
    'utf8',
  );
  const m = new Map<string, string[]>();
  for (const [, word, cls] of yaml.matchAll(
    /^\s*- \{word: "([a-z_]+)", class: (\w+)/gm,
  )) {
    m.set(cls, [...(m.get(cls) ?? []), word]);
  }
  return m;
}

test('every VHDL reserved word is a keyword in any case', () => {
  const words = keywordsByClass().get('vhdl') ?? [];
  assert.ok(words.length > 100, `only ${words.length} words read`);
  for (const w of words) {
    const mixed = w[0].toUpperCase() + w.slice(1);
    for (const form of [w, w.toUpperCase(), mixed]) {
      whole(`q <= ${form};`, form, 'keyword.');
    }
  }
});

test('PSL-only words and inherit are not keywords outside PSL', () => {
  const classes = keywordsByClass();
  const words = [
    ...(classes.get('psl') ?? []),
    ...(classes.get('psl2008') ?? []),
    ...(classes.get('never') ?? []),
  ];
  assert.ok(words.length > 50, `only ${words.length} words read`);
  for (const w of words) {
    assert.equal(scopeOf(`q <= ${w};`, w), '', w);
  }
});

test('a keyword inside an identifier is not a keyword', () => {
  for (const id of ['signal_a', 'end_of_line', 'ins', 'my_entity', 'is2']) {
    assert.equal(scopeOf(`${id} <= 1;`, id), '', id);
  }
});

test('comments', () => {
  whole('-- signal x : bit;', '--', 'punctuation.definition.comment');
  assert.match(scopeOf('-- signal x : bit;', ' signal'), /^comment\.line/);
  assert.match(
    scopeOf('s <= a; -- if then', ' if then'),
    /^comment\.line\.double-dash/,
  );
  const block = '/* begin\n end; */ signal';
  assert.match(scopeOf(block, ' begin', 0), /^comment\.block/);
  assert.match(scopeOf(block, ' end;', 1), /^comment\.block/);
  whole(block, 'signal', 'keyword.other', 1);
});

test('strings', () => {
  const src = 's := "a ""quoted"" -- not a comment";';
  whole(src, '""', 'constant.character.escape');
  assert.match(scopeOf(src, ' -- not'), /^string\.quoted\.double/);
  whole(src, ';', 'punctuation.terminator');
  // An unterminated string ends with its line.
  whole('s := "open\nbegin', 'begin', 'keyword.other', 1);
  whole('x <= "";', ';', 'punctuation.terminator');
});

test('character literals, attributes and qualified expressions', () => {
  whole("s <= '0';", "'0'", 'string.quoted.single');
  whole("c := ''';", "'''", 'string.quoted.single');
  whole("c := ' ';", "' '", 'string.quoted.single');
  whole("if clk'event then", 'event', 'entity.other.attribute-name');
  whole("n := bits(0)'length;", 'length', 'entity.other.attribute-name');
  whole("n := \\x y\\'length;", 'length', 'entity.other.attribute-name');
  const q = "s <= std_logic'('1');";
  whole(q, "'", 'punctuation.separator.qualified-expression');
  whole(q, "'1'", 'string.quoted.single');
  whole("v := (others => '1');", "'1'", 'string.quoted.single');
});

test('bit-string literals', () => {
  for (const lit of [
    'x"FF"',
    'B"1010_0101"',
    '12UX"F"',
    'sx"F"',
    'D"10"',
    'o"7"',
  ]) {
    whole(`v := ${lit};`, lit, 'string.quoted.double.bit-string');
  }
});

test('numbers', () => {
  for (const n of ['42', '1_000', '1.0e-3', '2.5E+6', '7E2']) {
    whole(`x := ${n};`, n, 'constant.numeric.decimal');
  }
  for (const n of ['16#FF#', '2#1010_0101#', '16#F.F#E+2', '8#777#']) {
    whole(`x := ${n};`, n, 'constant.numeric.based');
  }
  assert.equal(scopeOf('a1 <= b2;', 'a1'), '');
  whole('wait for 10 ns;', '10', 'constant.numeric.decimal');
});

test('extended identifiers hide keywords', () => {
  whole('\\signal\\ <= 1;', '\\signal\\', 'meta.identifier.extended');
  whole('\\a\\\\b\\ <= 1;', '\\a\\\\b\\', 'meta.identifier.extended');
});

test('tool directives', () => {
  const src = '`if TOOL_TYPE = "SIMULATION" then';
  whole(src, '`', 'punctuation.definition.directive');
  whole(src, 'then', 'keyword.control');
  assert.match(scopeOf('`end if', 'end'), /keyword\.control\.directive/);
});

test('operators and punctuation', () => {
  for (const op of ['<=', ':=', '=>', '?=', '?/=', '/=', '**', '<>', '&']) {
    whole(`a ${op} b`, op, 'keyword.operator');
  }
  whole(
    's <= <<signal .tb.dut.s : bit>>;',
    '<<',
    'punctuation.definition.external-name',
  );
  whole('x : bit;', ':', 'punctuation.separator.colon');
  whole('a, b', ',', 'punctuation.separator.comma');
  whole('work.pkg', '.', 'punctuation.accessor');
});

test('keyword groups', () => {
  whole('if a then', 'if', 'keyword.control');
  whole('x := a xor b;', 'xor', 'keyword.operator.word');
  whole('signal s : bit;', 'signal', 'keyword.other');
});
