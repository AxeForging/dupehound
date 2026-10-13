package services

import (
	"strings"
	"testing"

	"github.com/AxeForging/dupehound/domain"
)

// Tests for detection across the whole file rather than inside one function
// body at a time. Each case is a pattern measured against SonarQube on a real
// repository, where function-only detection found 29% (rails) and 57%
// (laravel) of the duplicated lines Sonar reports.

func detectWholeFile(files []TokenizedFile, minTokens int) []domain.Clone {
	clones, _ := DetectWithOptions(files, DetectOptions{MinTokens: minTokens, MinSimilarity: 1.0, WholeFile: true})
	return clones
}

func tokenize(path, src string) TokenizedFile {
	return BuildTokenizedFile(path, src, DetectLanguage(path))
}

// phpAccessors renders a class made only of small methods, like Laravel's
// AbstractPaginator: no single method is long enough to be a clone.
func phpAccessors(class string) string {
	var b strings.Builder
	b.WriteString("<?php\n\nclass " + class + "\n{\n")
	for _, f := range []string{"items", "perPage", "path", "query", "fragment", "pageName", "options", "total"} {
		b.WriteString("    public function " + f + "()\n    {\n        return $this->" + f + ";\n    }\n\n")
	}
	b.WriteString("}\n")
	return b.String()
}

func TestWholeFile_FindsCopiedClassMadeOfSmallMethods(t *testing.T) {
	files := []TokenizedFile{
		tokenize("AbstractPaginator.php", phpAccessors("AbstractPaginator")),
		tokenize("AbstractCursorPaginator.php", phpAccessors("AbstractCursorPaginator")),
	}

	if got := Detect(files, 50, 1.0); len(got) != 0 {
		t.Fatalf("fixture must be invisible to function-only detection, got %d clones", len(got))
	}

	clones := detectWholeFile(files, 50)
	if len(clones) != 1 {
		t.Fatalf("whole-file detection found %d clones, want 1 spanning the copied methods", len(clones))
	}
	if n := clones[0].LineCount; n < 30 {
		t.Errorf("clone covers %d lines, want the whole run of methods (>= 30)", n)
	}
}

// Rails tests are `test "…" do` blocks, not `def` methods.
func TestWholeFile_FindsDuplicatedBlockStyleTests(t *testing.T) {
	body := "    post :create, params: { user: { name: \"a\", email: \"b\" } }\n" +
		"    assert_response :success\n    assert_equal 1, User.count\n    assert_equal \"a\", User.last.name\n" +
		"    assert_equal \"b\", User.last.email\n    assert_nil flash[:error]\n"
	src := func(class string) string {
		return "class " + class + " < ActionController::TestCase\n  test \"creates a user\" do\n" + body + "  end\nend\n"
	}
	files := []TokenizedFile{tokenize("a_test.rb", src("UsersTest")), tokenize("b_test.rb", src("AdminsTest"))}

	if got := Detect(files, 30, 1.0); len(got) != 0 {
		t.Fatalf("fixture must be invisible to function-only detection, got %d clones", len(got))
	}
	if got := detectWholeFile(files, 30); len(got) != 1 {
		t.Errorf("whole-file detection found %d clones, want 1", len(got))
	}
}

// Outside a function body, structure alone is not duplication: every list of
// fields or constants has the same shape. Names must match there.
func TestWholeFile_SameShapedDeclarationsAreNotClones(t *testing.T) {
	fields := func(class string, names ...string) string {
		var b strings.Builder
		b.WriteString("class " + class + " {\n")
		for _, n := range names {
			b.WriteString("    private String " + n + ";\n    private int " + n + "Count;\n")
		}
		b.WriteString("}\n")
		return b.String()
	}
	files := []TokenizedFile{
		tokenize("User.java", fields("User", "name", "email", "phone", "city", "street", "zip")),
		tokenize("Order.java", fields("Order", "sku", "title", "vendor", "depot", "lane", "bin")),
	}
	if got := detectWholeFile(files, 30); len(got) != 0 {
		t.Errorf("declarations with different names reported as %d clones, want 0", len(got))
	}

	// The same declarations copied verbatim are duplication.
	files[1] = tokenize("Customer.java", fields("Customer", "name", "email", "phone", "city", "street", "zip"))
	if got := detectWholeFile(files, 30); len(got) != 1 {
		t.Errorf("verbatim copied declarations reported as %d clones, want 1", len(got))
	}
}

// Inside function bodies, renamed copies stay detectable in whole-file mode.
func TestWholeFile_StillFindsRenamedLogicInsideFunctions(t *testing.T) {
	fn := func(name, acc, item string) string {
		return "func " + name + "(xs []int, limit int) int {\n\t" + acc + " := 0\n\tfor _, " + item + " := range xs {\n" +
			"\t\tif " + item + " > limit {\n\t\t\t" + acc + " += " + item + " * 2\n\t\t} else {\n\t\t\t" + acc + "++\n\t\t}\n\t}\n\treturn " + acc + "\n}\n"
	}
	files := []TokenizedFile{
		tokenize("a.go", "package a\n\n"+fn("sumA", "total", "x")),
		tokenize("b.go", "package b\n\n"+fn("sumB", "acc", "value")),
	}
	clones := detectWholeFile(files, 30)
	if len(clones) != 1 || clones[0].Type != domain.CloneType2 {
		t.Errorf("renamed function body: got %d clones (%v), want 1 type-2", len(clones), clones)
	}
}

func TestWholeFile_ImportBlocksAreNotClones(t *testing.T) {
	tests := []struct {
		name string
		path string
		head string
	}{
		{"go import block", "x.go", "package app\n\nimport (\n\t\"context\"\n\t\"errors\"\n\t\"fmt\"\n\t\"io\"\n\t\"net/http\"\n\t\"os\"\n\t\"strings\"\n\t\"sync\"\n\t\"time\"\n)\n"},
		{"python imports", "x.py", "import os\nimport sys\nimport json\nimport logging\nfrom typing import (\n    Any,\n    Dict,\n    List,\n)\nfrom pathlib import Path\nfrom datetime import datetime\n"},
		{"typescript imports", "x.ts", "import { Injectable, Inject } from '@nestjs/common';\nimport {\n  Controller,\n  Get,\n  Post,\n} from '@nestjs/core';\nimport * as fs from 'fs';\nimport path from 'path';\nexport { a, b, c } from './abc';\n"},
		{"java imports", "X.java", "package com.example.app;\n\nimport java.util.List;\nimport java.util.Map;\nimport java.util.Set;\nimport java.io.IOException;\nimport java.io.InputStream;\nimport java.nio.file.Path;\n"},
		{"rust use", "x.rs", "use std::collections::{\n    HashMap,\n    HashSet,\n};\nuse std::io::{self, Read, Write};\nuse std::sync::Arc;\npub use crate::error::Error;\nextern crate serde;\n"},
		{"php use", "x.php", "<?php\n\nnamespace App\\Http;\n\nuse App\\Models\\User;\nuse App\\Models\\Order;\nuse Illuminate\\Http\\Request;\nuse Illuminate\\Support\\Collection;\nuse Illuminate\\Support\\Str;\n"},
		{"ruby require", "x.rb", "require \"json\"\nrequire \"set\"\nrequire \"time\"\nrequire \"logger\"\nrequire_relative \"helper\"\nrequire_relative \"config\"\nrequire_relative \"models\"\n"},
		{"c includes", "x.c", "#include <stdio.h>\n#include <stdlib.h>\n#include <string.h>\n#include <errno.h>\n#include <unistd.h>\n#include \"config.h\"\n#include \"util.h\"\n"},
		{"csharp using", "X.cs", "using System;\nusing System.Collections.Generic;\nusing System.IO;\nusing System.Linq;\nusing System.Text;\nusing System.Threading.Tasks;\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ext := tt.path[strings.LastIndex(tt.path, "."):]
			files := []TokenizedFile{tokenize("a"+ext, tt.head), tokenize("b"+ext, tt.head)}
			if n := len(files[0].Tokens); n < 12 {
				t.Fatalf("fixture too small to be meaningful: %d tokens", n)
			}
			if got := detectWholeFile(files, 12); len(got) != 0 {
				t.Errorf("identical import statements reported as %d clones, want 0", len(got))
			}
		})
	}
}

// A `using (…)` statement or a call named like an import keyword inside a
// function is code, not an import.
func TestWholeFile_ImportKeywordInsideFunctionIsCode(t *testing.T) {
	body := "        using (var stream = File.OpenRead(path))\n        using (var reader = new StreamReader(stream))\n        {\n" +
		"            var text = reader.ReadToEnd();\n            Console.WriteLine(text.Length);\n            return text.Trim();\n        }\n"
	src := func(class string) string {
		return "class " + class + " {\n    string Load(string path)\n    {\n" + body + "    }\n}\n"
	}
	files := []TokenizedFile{tokenize("A.cs", src("A")), tokenize("B.cs", src("B"))}
	if got := detectWholeFile(files, 25); len(got) != 1 {
		t.Errorf("duplicated using-statements in a method body: got %d clones, want 1", len(got))
	}
}

func TestWholeFile_RespectsInlineIgnore(t *testing.T) {
	fn := "func process(xs []int, limit int) int {\n\ttotal := 0\n\tfor _, x := range xs {\n\t\tif x > limit {\n" +
		"\t\t\ttotal += x * 2\n\t\t} else {\n\t\t\ttotal++\n\t\t}\n\t}\n\treturn total\n}\n"
	build := func(path, marker string) TokenizedFile {
		return BuildTokenizedFileWithIgnore(path, "package p\n\n"+marker+fn, LangForName("go"), nil)
	}

	plain := []TokenizedFile{build("a.go", ""), build("b.go", "")}
	if got := detectWholeFile(plain, 30); len(got) != 1 {
		t.Fatalf("fixture must be a clone without the marker, got %d", len(got))
	}
	ignored := []TokenizedFile{build("a.go", "// dupehound:ignore\n"), build("b.go", "// dupehound:ignore\n")}
	if got := detectWholeFile(ignored, 30); len(got) != 0 {
		t.Errorf("clone reported under a dupehound:ignore marker: %d", len(got))
	}
}

// Found in rails: hundreds of one-statement tests that differ only in the
// strings they pass. Sonar reports them; names are identical, values are not.
func TestWholeFile_FindsBlockTestsDifferingOnlyInLiterals(t *testing.T) {
	tests := func(values ...string) string {
		var b strings.Builder
		b.WriteString("class ConversionTest < ActiveSupport::TestCase\n")
		for _, v := range values {
			b.WriteString("  test \"converts " + v + "\" do\n    assert_equal \"[" + v + "]\", Conversion.markdown_link(\"" + v + "\", \"https://" + v + ".example\")\n  end\n\n")
		}
		b.WriteString("end\n")
		return b.String()
	}
	files := []TokenizedFile{
		tokenize("a_test.rb", tests("alpha", "bravo", "charlie", "delta")),
		tokenize("b_test.rb", tests("echo", "foxtrot", "golf", "hotel")),
	}
	if got := detectWholeFile(files, 40); len(got) == 0 {
		t.Error("tests differing only in string values were not reported")
	}
}

// A table of data is not logic: two different tables of the same shape are not
// duplicates of each other, though a table copied verbatim is.
func TestWholeFile_DifferentDataTablesAreNotClones(t *testing.T) {
	table := func(prefix string) string {
		var b strings.Builder
		b.WriteString("<?php\n\nreturn [\n")
		for _, k := range []string{"accepted", "active", "after", "alpha", "array", "before", "between", "boolean", "confirmed", "date"} {
			b.WriteString("    '" + k + "' => '" + prefix + " " + k + " message.',\n")
		}
		b.WriteString("];\n")
		return b.String()
	}
	files := []TokenizedFile{tokenize("lang/en/validation.php", table("The")), tokenize("lang/es/validation.php", table("El"))}
	if got := detectWholeFile(files, 30); len(got) != 0 {
		t.Errorf("two different translation tables reported as %d clones, want 0", len(got))
	}
	files[1] = tokenize("lang/en_GB/validation.php", table("The"))
	if got := detectWholeFile(files, 30); len(got) != 1 {
		t.Errorf("a table copied verbatim reported as %d clones, want 1", len(got))
	}
}

// A whole-file clone may begin in a signature or run across several functions.
// It is attributed to the function it overlaps most, not left anonymous.
func TestWholeFile_AttributesCloneToTheFunctionItMostlyCovers(t *testing.T) {
	fn := func(name string) string {
		return "func " + name + "(xs []int, limit int) int {\n\ttotal := 0\n\tfor _, x := range xs {\n\t\tif x > limit {\n" +
			"\t\t\ttotal += x * 2\n\t\t} else {\n\t\t\ttotal++\n\t\t}\n\t}\n\treturn total\n}\n"
	}
	files := []TokenizedFile{
		tokenize("a.go", "package a\n\n"+fn("helper")),
		tokenize("b.go", "package b\n\n"+fn("worker")),
	}
	clones := detectWholeFile(files, 30)
	if len(clones) != 1 {
		t.Fatalf("got %d clones, want 1", len(clones))
	}
	if a, b := clones[0].Instances[0].Function, clones[0].Instances[1].Function; a != "helper" || b != "worker" {
		t.Errorf("attributed to %q and %q, want helper and worker", a, b)
	}
}
