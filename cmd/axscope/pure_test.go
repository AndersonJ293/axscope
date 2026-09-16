package main

import (
	"os"
	"path/filepath"
	"testing"
)

// human formats a byte count for the clean output; the boundaries between units
// are where a wrong divisor or unit letter shows up.
func TestHuman(t *testing.T) {
	cases := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{3 * 1024 * 1024, "3.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
	}
	for _, c := range cases {
		if got := human(c.bytes); got != c.want {
			t.Errorf("human(%d) = %q, want %q", c.bytes, got, c.want)
		}
	}
}

// dirSize adds the files of a tree and ignores directories; a missing path is
// simply zero (clean relies on it to skip what is not there).
func TestDirSize(t *testing.T) {
	if got := dirSize(filepath.Join(t.TempDir(), "does-not-exist")); got != 0 {
		t.Errorf("dirSize(missing) = %d, want 0", got)
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "b.txt"), []byte("world!"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := dirSize(root); got != 11 {
		t.Errorf("dirSize = %d, want 11", got)
	}
}

func TestEnvBoolAndEnvOr(t *testing.T) {
	boolCases := []struct {
		env  string
		def  bool
		want bool
	}{
		{"1", false, true},
		{"true", false, true},
		{"yes", false, true},
		{"on", false, true},
		{"0", true, false},
		{"false", true, false},
		{"no", true, false},
		{"off", true, false},
		{"", true, true},
		{"", false, false},
		{"maybe", true, true},
	}
	for _, c := range boolCases {
		t.Run("bool "+c.env, func(t *testing.T) {
			t.Setenv("AXSCOPE_TEST_BOOL", c.env)
			if got := envBool("AXSCOPE_TEST_BOOL", c.def); got != c.want {
				t.Errorf("envBool(%q, %v) = %v, want %v", c.env, c.def, got, c.want)
			}
		})
	}

	t.Run("envOr set", func(t *testing.T) {
		t.Setenv("AXSCOPE_TEST_STR", "ext")
		if got := envOr("AXSCOPE_TEST_STR", "chrome"); got != "ext" {
			t.Errorf("envOr = %q, want ext", got)
		}
	})
	t.Run("envOr unset", func(t *testing.T) {
		t.Setenv("AXSCOPE_TEST_STR", "")
		if got := envOr("AXSCOPE_TEST_STR", "chrome"); got != "chrome" {
			t.Errorf("envOr = %q, want chrome", got)
		}
	})
}
