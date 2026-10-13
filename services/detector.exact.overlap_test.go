package services

import "testing"

// Tests for the instance-overlap guard (issue #31). Seed windows inside one
// file are spaced minTokens apart, but greedy extension grows every block to
// totalTokens — so without this guard a clone group reports the same stretch of
// text several times as if it were several duplicates.

func TestDropOverlappingStarts(t *testing.T) {
	tests := []struct {
		name        string
		starts      []globalPos
		totalTokens int
		want        []globalPos
	}{
		{
			name:        "already disjoint instances are all kept",
			starts:      []globalPos{{0, 0}, {0, 100}, {0, 200}},
			totalTokens: 50,
			want:        []globalPos{{0, 0}, {0, 100}, {0, 200}},
		},
		{
			name:        "instances exactly touching are disjoint",
			starts:      []globalPos{{0, 0}, {0, 50}, {0, 100}},
			totalTokens: 50,
			want:        []globalPos{{0, 0}, {0, 50}, {0, 100}},
		},
		{
			name:        "one token of overlap drops the instance",
			starts:      []globalPos{{0, 0}, {0, 49}},
			totalTokens: 50,
			want:        []globalPos{{0, 0}},
		},
		{
			name:        "a run of overlapping instances collapses to the survivors",
			starts:      []globalPos{{0, 0}, {0, 30}, {0, 60}, {0, 90}},
			totalTokens: 50,
			want:        []globalPos{{0, 0}, {0, 60}},
		},
		{
			name:        "instances in different files never overlap",
			starts:      []globalPos{{0, 0}, {1, 10}, {2, 20}},
			totalTokens: 50,
			want:        []globalPos{{0, 0}, {1, 10}, {2, 20}},
		},
		{
			name:        "overlap is judged per file",
			starts:      []globalPos{{0, 0}, {0, 10}, {1, 0}, {1, 10}},
			totalTokens: 50,
			want:        []globalPos{{0, 0}, {1, 0}},
		},
		{
			name:        "the first instance is always kept",
			starts:      []globalPos{{0, 7}, {0, 8}, {0, 9}},
			totalTokens: 50,
			want:        []globalPos{{0, 7}},
		},
		{
			name:        "single instance passes through",
			starts:      []globalPos{{0, 0}},
			totalTokens: 50,
			want:        []globalPos{{0, 0}},
		},
		{
			name:        "empty input yields empty output",
			starts:      []globalPos{},
			totalTokens: 50,
			want:        []globalPos{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := dropOverlappingStarts(tc.starts, tc.totalTokens)
			if len(got) != len(tc.want) {
				t.Fatalf("kept %d instances %v, want %d %v", len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("instance %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// The guard must not weaken genuine detection: a file with several truly
// separate copies of a block still reports all of them.
func TestDetect_RepeatedDisjointBlocksAllReported(t *testing.T) {
	block := `
	conn := open(addr)
	if conn == nil {
		panic("no conn")
	}
	rows := conn.Query(stmt)
	for rows.Next() {
		var id int
		var name string
		rows.Scan(&id, &name)
		emit(id, name)
	}
	rows.Close()
	conn.Close()
`
	// Three separate functions, each holding one copy — unambiguously disjoint.
	src := "package main\n" +
		"func alpha() {" + block + "}\n" +
		"func bravo() {" + block + "}\n" +
		"func charlie() {" + block + "}\n"

	clones := Detect([]TokenizedFile{makeFile("a.go", src)}, 50, 1.0)
	if len(clones) == 0 {
		t.Fatal("three copies of the same block must be reported")
	}
	assertNoOverlappingInstances(t, clones)

	best := 0
	for _, c := range clones {
		if len(c.Instances) > best {
			best = len(c.Instances)
		}
	}
	if best < 3 {
		t.Errorf("largest clone group has %d instances, want 3 (one per copy)", best)
	}
}

// Cross-file groups have one instance per file and so can never overlap; the
// guard must leave them untouched.
func TestDetect_CrossFileInstancesUnaffectedByOverlapGuard(t *testing.T) {
	block := "func work() {\n\tx := compute(a, b)\n\ty := refine(x)\n\tz := combine(x, y)\n\treport(z)\n\tflush(z)\n\tclose(z)\n}\n"
	files := []TokenizedFile{
		makeFile("a.go", "package main\n"+block),
		makeFile("b.go", "package main\n"+block),
		makeFile("c.go", "package main\n"+block),
	}

	clones := Detect(files, 20, 1.0)
	if len(clones) == 0 {
		t.Fatal("identical block across three files must be reported")
	}
	assertNoOverlappingInstances(t, clones)

	for _, c := range clones {
		if len(c.Instances) != 3 {
			t.Errorf("clone %s has %d instances, want 3 (one per file)", c.Hash, len(c.Instances))
		}
	}
}
