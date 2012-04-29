package main

import (
	"fmt"
	"reflect"
	"regexp"
	"runtime"
	"testing"
)

type ReplaceFirstTest struct {
	search         string
	replace        string
	testInput      string
	expectedOutput string
}

var testFirstReplacements = []ReplaceFirstTest{
	// search, replace, input, output
	{"a", "b", "a", "b"},
	{"a", "b", "c", "c"},
	{"(a)", "b", "a", "b"},
	{"b(a)", "$1", "ba", "a"},
	{"(a)", "$1$1$1", "a", "aaa"},
	{"(a)", "$1$1$1", "bab", "baaab"},
	{"(.)(.)", "$2$1", "ab", "ba"},
}

func TestReplaceFirst(t *testing.T) {
	for _, rep := range testFirstReplacements {
		ExpectEquals(
			t,
			rep.expectedOutput,
			ReplaceFirst(regexp.MustCompile(rep.search), rep.replace, rep.testInput))

	}
}

type SnippetTest struct {
	snippet         string
	expectedReplace string
	testInput       string
	expectedOutput  string
}

var (
	testSnippets = []SnippetTest{
		// substitution, replace, input, output
		{`s/a/b/`, `b`, `aaa`, `baa`},
		{`s/\/a/b/`, `b`, `a/aa`, `aba`},
		{`s/a/b/g`, `b`, `sababa!`, `sbbbbb!`},
		{`s/\bi\b/me/i`, `me`, `give you and I`, `give you and me`},
	}
)

func TestImportSnippet(t *testing.T) {
	for _, s := range testSnippets {
		ok := importSnippet([]string{s.snippet})
		imported := substitutionVals[0]
		ExpectEquals(t, true, ok, "substitution parse", s.snippet)
		ExpectEquals(t, s.expectedReplace, imported.replace, "substitution parsed replacement", s.snippet)
		actualOutput := substitute(imported, s.testInput)
		ExpectEquals(t, s.expectedOutput, actualOutput, "snippeting", s.snippet, imported.search.String())
		substitutionVals = substitutionVals[0:0]
	}
}

func ExpectEquals(t *testing.T, expected, actual interface{}, desc ...string) {
	if !reflect.DeepEqual(expected, actual) {
		_, file, line, _ := runtime.Caller(1)
		desc1 := fmt.Sprintf("%s:%d", file, line)
		if len(desc) > 0 {
			desc1 += " " + fmt.Sprint(desc)
		}
		t.Errorf("%s\nExpected: %#v\nActual:   %#v\n", desc1, expected, actual)
	}
}
