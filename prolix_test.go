package main

import (
	"testing"
)

func TestImportSnippet(t *testing.T) {
	testSnippets := []struct {
		snippet     string
		wantReplace string
		testInput   string
		wantOutput  string
	}{
		// substitution, replace, input, output
		{`s/a/b/`, `b`, `aaa`, `baa`},
		{`s/\/a/b/`, `b`, `a/aa`, `aba`},
		{`s/a/b/g`, `b`, `sababa!`, `sbbbbb!`},
		{`s/\bi\b/me/i`, `me`, `give you and I`, `give you and me`},
	}

	for _, s := range testSnippets {
		substitutionVals = substitutionVals[:0] // Reset substitutions
		ok := importSnippet([]string{s.snippet})
		imported := substitutionVals[0]

		if !ok {
			t.Fatalf("substitution parse: %q", s.snippet)
		}
		if imported.replace != s.wantReplace {
			t.Errorf("parsed replacement: %q, got=%q, want=%q", s.snippet, imported.replace, s.wantReplace)
		}
		gotOutput := substitute(imported, s.testInput)
		if gotOutput != s.wantOutput {
			t.Errorf("snippeting: %q=>%q\nhave=%q\nwant=%q", s.snippet, imported.search, gotOutput, s.wantOutput)
		}
	}
}
