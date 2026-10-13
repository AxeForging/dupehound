package services

import "testing"

// Table-driven tests are idiomatic well beyond Go, so the data-literal rule
// covers every language whose collection literals can be identified from tokens
// alone. These tests pin down, per language, both directions: the table is
// recognised as data, and the code around it is not.

// Each fixture wraps a probe token in a collection literal, and another probe in
// ordinary logic, so one source exercises both classifications.
func TestDataLiteralSpans_PerLanguage(t *testing.T) {
	tests := []struct {
		name string
		lang string
		src  string
		// data must be classified as table data; logic must not.
		data  string
		logic string
	}{
		{
			name:  "python list of dicts",
			lang:  "python",
			src:   "def test():\n    cases = [\n        {'name': DATA, 'want': 1},\n        {'name': 'b', 'want': 2},\n    ]\n    for c in cases:\n        LOGIC(c)\n",
			data:  "DATA",
			logic: "LOGIC",
		},
		{
			name:  "python dict literal",
			lang:  "python",
			src:   "def test():\n    m = {'a': DATA, 'b': 2}\n    LOGIC(m)\n",
			data:  "DATA",
			logic: "LOGIC",
		},
		{
			name:  "javascript array of objects",
			lang:  "javascript",
			src:   "function test() {\n  const cases = [\n    {name: DATA, want: 1},\n    {name: 'b', want: 2},\n  ];\n  cases.forEach(c => LOGIC(c));\n}\n",
			data:  "DATA",
			logic: "LOGIC",
		},
		{
			name:  "typescript array of objects",
			lang:  "typescript",
			src:   "function test() {\n  const cases = [\n    {name: DATA, want: 1},\n    {name: 'b', want: 2},\n  ];\n  for (const c of cases) { LOGIC(c); }\n}\n",
			data:  "DATA",
			logic: "LOGIC",
		},
		{
			name:  "ruby array of hashes",
			lang:  "ruby",
			src:   "def test\n  cases = [\n    {name: DATA, want: 1},\n    {name: 'b', want: 2},\n  ]\n  cases.each { |c| LOGIC(c) }\nend\n",
			data:  "DATA",
			logic: "LOGIC",
		},
		{
			name:  "rust vec macro",
			lang:  "rust",
			src:   "fn test() {\n    let cases = vec![\n        (DATA, 1),\n        (\"b\", 2),\n    ];\n    for c in cases {\n        LOGIC(c);\n    }\n}\n",
			data:  "DATA",
			logic: "LOGIC",
		},
		{
			name:  "php array literal",
			lang:  "php",
			src:   "function test() {\n    $cases = [\n        ['name' => DATA, 'want' => 1],\n        ['name' => 'b', 'want' => 2],\n    ];\n    foreach ($cases as $c) {\n        LOGIC($c);\n    }\n}\n",
			data:  "DATA",
			logic: "LOGIC",
		},
		{
			name:  "elixir list of tuples",
			lang:  "elixir",
			src:   "def test do\n  cases = [\n    {DATA, 1},\n    {:b, 2}\n  ]\n  Enum.each(cases, fn c -> LOGIC(c) end)\nend\n",
			data:  "DATA",
			logic: "LOGIC",
		},
		{
			name:  "swift array of tuples",
			lang:  "swift",
			src:   "func test() {\n    let cases = [\n        (name: DATA, want: 1),\n        (name: \"b\", want: 2),\n    ]\n    for c in cases {\n        LOGIC(c)\n    }\n}\n",
			data:  "DATA",
			logic: "LOGIC",
		},
		{
			name:  "dart list of maps",
			lang:  "dart",
			src:   "void test() {\n  var cases = [\n    {'name': DATA, 'want': 1},\n    {'name': 'b', 'want': 2},\n  ];\n  for (var c in cases) {\n    LOGIC(c);\n  }\n}\n",
			data:  "DATA",
			logic: "LOGIC",
		},
		{
			name:  "lua table constructor",
			lang:  "lua",
			src:   "function test()\n  local cases = {\n    {name = DATA, want = 1},\n    {name = \"b\", want = 2},\n  }\n  for _, c in ipairs(cases) do\n    LOGIC(c)\n  end\nend\n",
			data:  "DATA",
			logic: "LOGIC",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lang := LangForName(tc.lang)
			if lang == nil {
				t.Fatalf("language %q is not supported by the scanner", tc.lang)
			}
			p := probeDetectionMaskLang(tc.src, lang)
			p.wantMasked(t, tc.data, true)
			p.wantMasked(t, tc.logic, false)
		})
	}
}

// Indexing uses the same bracket a list literal does. Mistaking `m[k]` for a
// literal would let the rule silence real code, so every bracket language is
// checked against its own indexing syntax.
func TestDataLiteralSpans_IndexingIsNotALiteral(t *testing.T) {
	tests := []struct {
		name string
		lang string
		src  string
	}{
		{
			name: "python subscript",
			lang: "python",
			src:  "def f(m, k):\n    if m[k]:\n        LOGIC()\n",
		},
		{
			name: "javascript index",
			lang: "javascript",
			src:  "function f(m, k) {\n  if (m[k]) {\n    LOGIC();\n  }\n}\n",
		},
		{
			name: "javascript index on call result",
			lang: "javascript",
			src:  "function f() {\n  if (rows()[0]) {\n    LOGIC();\n  }\n}\n",
		},
		{
			name: "ruby index",
			lang: "ruby",
			src:  "def f(m, k)\n  if m[k]\n    LOGIC()\n  end\nend\n",
		},
		{
			name: "rust index",
			lang: "rust",
			src:  "fn f(m: &[i32]) {\n    if m[0] > 0 {\n        LOGIC();\n    }\n}\n",
		},
		{
			name: "php index",
			lang: "php",
			src:  "function f($m, $k) {\n    if ($m[$k]) {\n        LOGIC();\n    }\n}\n",
		},
		{
			name: "dart index",
			lang: "dart",
			src:  "void f(m, k) {\n  if (m[k]) {\n    LOGIC();\n  }\n}\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lang := LangForName(tc.lang)
			if lang == nil {
				t.Fatalf("language %q is not supported by the scanner", tc.lang)
			}
			probeDetectionMaskLang(tc.src, lang).wantMasked(t, "LOGIC", false)
		})
	}
}

// Logic written inside a table — a callback column — is still logic. The
// exemption has to survive each language's own closure syntax.
func TestDataLiteralSpans_CallbacksInsideTablesStayVisible(t *testing.T) {
	tests := []struct {
		name string
		lang string
		src  string
	}{
		{
			name: "javascript arrow function in a table row",
			lang: "javascript",
			src:  "function test() {\n  const cases = [\n    {name: 'a', run: () => { LOGIC(); }},\n    {name: 'b', run: () => { other(); }},\n  ];\n}\n",
		},
		{
			name: "javascript function expression in a table row",
			lang: "javascript",
			src:  "function test() {\n  const cases = [\n    {name: 'a', run: function () { LOGIC(); }},\n  ];\n}\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lang := LangForName(tc.lang)
			if lang == nil {
				t.Fatalf("language %q is not supported by the scanner", tc.lang)
			}
			probeDetectionMaskLang(tc.src, lang).wantMasked(t, "LOGIC", false)
		})
	}
}

// End-to-end per language: the table is not reported, and genuine duplication in
// the same file still is. Without the second half, "no clones" could just mean
// detection broke for that language.
func TestDetect_TableDrivenTestsAcrossLanguages(t *testing.T) {
	tests := []struct {
		name  string
		lang  string
		file  string
		table string
		dup   string
	}{
		{
			name: "python",
			lang: "python",
			file: "test_things.py",
			table: `def test_presets():
    cases = [
        {'name': 'openai', 'token': 'openai_AAAA0', 'want': 'openai_***'},
        {'name': 'anthropic', 'token': 'anthropic_AAAA1', 'want': 'anthropic_***'},
        {'name': 'github', 'token': 'github_AAAA2', 'want': 'github_***'},
        {'name': 'awskey', 'token': 'awskey_AAAA3', 'want': 'awskey_***'},
        {'name': 'bearer', 'token': 'bearer_AAAA4', 'want': 'bearer_***'},
        {'name': 'stripe', 'token': 'stripe_AAAA5', 'want': 'stripe_***'},
        {'name': 'gcp', 'token': 'gcp_AAAA6', 'want': 'gcp_***'},
        {'name': 'azure', 'token': 'azure_AAAA7', 'want': 'azure_***'},
    ]
    for c in cases:
        assert redact(c['token']) == c['want']
`,
			dup: `    server = start_server()
    client = server.client()
    request = build_request(server.url)
    response = client.send(request)
    assert response.status == 200
    body = response.read_all()
    assert body is not None
    server.close()
    log_result(body)
`,
		},
		{
			name: "javascript",
			lang: "javascript",
			file: "things.test.js",
			table: `function testPresets() {
  const cases = [
    {name: 'openai', token: 'openai_AAAA0', want: 'openai_***'},
    {name: 'anthropic', token: 'anthropic_AAAA1', want: 'anthropic_***'},
    {name: 'github', token: 'github_AAAA2', want: 'github_***'},
    {name: 'awskey', token: 'awskey_AAAA3', want: 'awskey_***'},
    {name: 'bearer', token: 'bearer_AAAA4', want: 'bearer_***'},
    {name: 'stripe', token: 'stripe_AAAA5', want: 'stripe_***'},
    {name: 'gcp', token: 'gcp_AAAA6', want: 'gcp_***'},
    {name: 'azure', token: 'azure_AAAA7', want: 'azure_***'},
  ];
  cases.forEach(c => expect(redact(c.token)).toBe(c.want));
}
`,
			dup: `  const server = startServer();
  const client = server.client();
  const request = buildRequest(server.url);
  const response = client.send(request);
  expect(response.status).toBe(200);
  const body = response.readAll();
  expect(body).toBeDefined();
  server.close();
  logResult(body);
`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lang := LangForName(tc.lang)

			// The table alone must produce nothing.
			tableOnly := BuildTokenizedFile(tc.file, tc.table, lang)
			if clones := Detect([]TokenizedFile{tableOnly}, 50, 0.70); len(clones) != 0 {
				t.Errorf("%s table-driven test reported %d clone(s); want a clean scan", tc.lang, len(clones))
				for _, c := range clones {
					for _, in := range c.Instances {
						t.Logf("    %s:%d-%d", in.File, in.StartLine, in.EndLine)
					}
				}
			}

			// Genuine duplication in the same language must still be reported.
			var withDup string
			switch tc.lang {
			case "python":
				withDup = tc.table + "\ndef test_alpha():\n" + tc.dup + "\ndef test_bravo():\n" + tc.dup
			case "javascript":
				withDup = tc.table + "\nfunction testAlpha() {\n" + tc.dup + "}\n\nfunction testBravo() {\n" + tc.dup + "}\n"
			}
			dupFile := BuildTokenizedFile(tc.file, withDup, lang)
			clones := Detect([]TokenizedFile{dupFile}, 50, 0.70)
			if len(clones) == 0 {
				t.Fatalf("%s: copy-pasted logic alongside a table must still be reported", tc.lang)
			}
			assertNoOverlappingInstances(t, clones)
		})
	}
}
