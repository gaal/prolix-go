package main

import (
	"testing"

	tu "github.com/gaal/go-util/testingutil"
)

func TestImportSnippet(t *testing.T) {
	testSnippets := []struct {
		snippet         string
		expectedReplace string
		testInput       string
		expectedOutput  string
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

		tu.ExpectEqual(t, ok, true, "substitution parse: %q", s.snippet)
		tu.ExpectEqual(
			t, imported.replace, s.expectedReplace,
			"parsed replacement: %q", s.snippet)
		actualOutput := substitute(imported, s.testInput)
		tu.ExpectEqual(
			t, actualOutput, s.expectedOutput,
			"snippeting: %q=>%q", s.snippet, imported.search.String())
	}
}
