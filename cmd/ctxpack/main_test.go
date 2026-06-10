package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunVersionFlag(t *testing.T) {
	if code := run([]string{"--version"}); code != 0 {
		t.Fatalf("code = %d", code)
	}
}

func TestRunFixtureJSON(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fixture := filepath.Join("..", "..", "tests", "fixture.html")
	if code := run([]string{fixture, "--json"}); code != 0 {
		t.Fatalf("code = %d", code)
	}
}

func TestRunSubcommandForm(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fixture := filepath.Join("..", "..", "tests", "fixture.html")
	if code := run([]string{"run", fixture, "--json"}); code != 0 {
		t.Fatalf("code = %d", code)
	}
}

func TestRunOutputWritesFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fixture := filepath.Join("..", "..", "tests", "fixture.html")
	out := filepath.Join(t.TempDir(), "out.md")
	if code := run([]string{fixture, "--no-record", "-o", out}); code != 0 {
		t.Fatalf("code = %d", code)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Fatal("empty output file")
	}
}

func TestRunMissingFileReturns2(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if code := run([]string{filepath.Join(t.TempDir(), "does-not-exist.html"), "--no-record"}); code != 2 {
		t.Fatalf("code = %d", code)
	}
}

func TestRunResetRequiresYes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if code := run([]string{"reset"}); code != 2 {
		t.Fatalf("code = %d", code)
	}
}
