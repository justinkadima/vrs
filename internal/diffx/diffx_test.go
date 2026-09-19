package diffx

import "testing"

func render(a, b string, n int) string {
	return Unified(LineDiff(a, b), n)
}

func TestUnified(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		n    int
		want string
	}{
		{"no change", "x\n", "x\n", 3, ""},
		{"modify middle", "1\n2\n3\n", "1\nX\n3\n", 3,
			"@@ -1,3 +1,3 @@\n 1\n-2\n+X\n 3\n"},
		{"insert middle", "1\n2\n3\n", "1\n2\nN\n3\n", 3,
			"@@ -1,3 +1,4 @@\n 1\n 2\n+N\n 3\n"},
		{"delete middle", "1\n2\n3\n4\n5\n", "1\n2\n4\n5\n", 3,
			"@@ -1,5 +1,4 @@\n 1\n 2\n-3\n 4\n 5\n"},
		{"insert at top, no context", "2\n3\n", "1\n2\n3\n", 0,
			"@@ -0,0 +1 @@\n+1\n"},
		{"single change, no context", "1\n2\n", "X\n2\n", 0,
			"@@ -1 +1 @@\n-1\n+X\n"},
		{"insert at end", "1\n", "1\n2\n", 3,
			"@@ -1 +1,2 @@\n 1\n+2\n"},
		{"two hunks", "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n", "1\nX\n3\n4\n5\n6\n7\n8\nY\n10\n", 1,
			"@@ -1,3 +1,3 @@\n 1\n-2\n+X\n 3\n@@ -8,3 +8,3 @@\n 8\n-9\n+Y\n 10\n"},
		{"merge when close", "1\n2\n3\n4\n5\n", "1\nX\n3\n4\nY\n", 1,
			"@@ -1,5 +1,5 @@\n 1\n-2\n+X\n 3\n 4\n-5\n+Y\n"},
		{"empty to something", "", "a\nb\n", 3,
			"@@ -0,0 +1,2 @@\n+a\n+b\n"},
		{"something to empty", "a\nb\n", "", 3,
			"@@ -1,2 +0,0 @@\n-a\n-b\n"},
	}
	for _, c := range cases {
		if got := render(c.a, c.b, c.n); got != c.want {
			t.Errorf("%s:\nwant:\n%s\ngot:\n%s", c.name, c.want, got)
		}
	}
}
