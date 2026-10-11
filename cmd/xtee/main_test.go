// Package main_test exercises the xtee command end to end: every test runs the
// freshly built binary as a subprocess with piped stdin and asserts on exit
// status, stdout, and resulting file contents. The binary is built with the
// race detector when the toolchain supports it, so any data race in a test
// scenario fails that test.
package main_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var xteePath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "xtee-test")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create temp dir: %v\n", err)
		os.Exit(1)
	}

	xteePath = filepath.Join(dir, "xtee")

	if !buildBinary(xteePath) {
		os.RemoveAll(dir)
		os.Exit(1)
	}

	code := m.Run()

	os.RemoveAll(dir)
	os.Exit(code)
}

func buildBinary(path string) (ok bool) {
	ctx := context.Background()

	build := exec.CommandContext(ctx, "go", "build", "-race", "-o", path, ".")

	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "race build failed (%v), falling back to plain build:\n%s\n", err, out)

		build = exec.CommandContext(ctx, "go", "build", "-o", path, ".")

		if out, err := build.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "build: %v\n%s\n", err, out)

			return false
		}
	}

	return true
}

func runXtee(t *testing.T, stdin io.Reader, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), xteePath, args...)

	cmd.Stdin = stdin

	var outBuf, errBuf bytes.Buffer

	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err = cmd.Run()

	return outBuf.String(), errBuf.String(), err
}

func readFile(t *testing.T, path string) (content string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	return string(data)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestStreamBasic(t *testing.T) {
	t.Parallel()

	dest := filepath.Join(t.TempDir(), "out.txt")

	stdout, _, err := runXtee(t, strings.NewReader("zero\none\n"), dest)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := readFile(t, dest); got != "zero\none\n" {
		t.Errorf("file = %q, want %q", got, "zero\\none\\n")
	}

	if stdout != "zero\none\n" {
		t.Errorf("stdout = %q, want input echoed", stdout)
	}
}

func TestStreamUnique(t *testing.T) {
	t.Parallel()

	dest := filepath.Join(t.TempDir(), "out.txt")

	stdout, _, err := runXtee(t, strings.NewReader("a\nb\na\nc\nb\n"), "--unique", dest)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := readFile(t, dest); got != "a\nb\nc\n" {
		t.Errorf("file = %q, want %q", got, "a\\nb\\nc\\n")
	}

	if stdout != "a\nb\nc\n" {
		t.Errorf("stdout = %q, want deduplicated input in input order", stdout)
	}
}

func TestAppendUnique(t *testing.T) {
	t.Parallel()

	dest := filepath.Join(t.TempDir(), "stuff.txt")

	writeFile(t, dest, "one\ntwo\nthree\n")

	stdout, _, err := runXtee(t, strings.NewReader("zero\none\ntwo\nthree\nfour\nfive\n"), "--append", "--unique", dest)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := readFile(t, dest); got != "one\ntwo\nthree\nzero\nfour\nfive\n" {
		t.Errorf("file = %q, want existing + new unique lines", got)
	}

	if stdout != "zero\nfour\nfive\n" {
		t.Errorf("stdout = %q, want only the appended lines", stdout)
	}
}

// TestSoakInPlace is the regression test for reading from and writing to the
// same file with --soak: the destination must not be truncated before stdin
// has been buffered. Previously this emptied the file.
func TestSoakInPlace(t *testing.T) {
	t.Parallel()

	dest := filepath.Join(t.TempDir(), "stuff.txt")

	writeFile(t, dest, "zero\none\ntwo\nthree\nzero\nfour\nfive\nfive\n")

	stdin := strings.NewReader(readFile(t, dest))

	if _, _, err := runXtee(t, stdin, "--soak", "--unique", "--quiet", dest); err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := readFile(t, dest); got != "zero\none\ntwo\nthree\nfour\nfive\n" {
		t.Errorf("file = %q, want deduplicated content", got)
	}
}

// TestSoakLargeDeterministic is the regression test for the removed worker
// pool: soaked output must equal the input exactly — complete, ordered, and
// free of corruption. Built with -race, any data race also fails this test.
func TestSoakLargeDeterministic(t *testing.T) {
	t.Parallel()

	var sb strings.Builder

	for i := 1; i <= 200000; i++ {
		sb.WriteString(strconv.Itoa(i))
		sb.WriteByte('\n')
	}

	want := sb.String()
	dest := filepath.Join(t.TempDir(), "out.txt")

	if _, _, err := runXtee(t, strings.NewReader(want), "--soak", "--unique", "--quiet", dest); err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := readFile(t, dest); got != want {
		t.Errorf("file content differs from input: got %d bytes, want %d", len(got), len(want))
	}
}

func TestSoakAppendUnique(t *testing.T) {
	t.Parallel()

	dest := filepath.Join(t.TempDir(), "stuff.txt")

	writeFile(t, dest, "a\nb\n")

	if _, _, err := runXtee(t, strings.NewReader("c\na\nd\n"), "--soak", "--append", "--unique", "--quiet", dest); err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := readFile(t, dest); got != "a\nb\nc\nd\n" {
		t.Errorf("file = %q, want %q", got, "a\\nb\\nc\\nd\\n")
	}
}

func TestPreview(t *testing.T) {
	t.Parallel()

	t.Run("NoDestination", func(t *testing.T) {
		t.Parallel()

		stdout, _, err := runXtee(t, strings.NewReader("x\ny\n"), "--preview")
		if err != nil {
			t.Fatalf("run: %v", err)
		}

		if stdout != "x\ny\n" {
			t.Errorf("stdout = %q, want input echoed", stdout)
		}
	})

	t.Run("UniqueAppendReflectsExistingFile", func(t *testing.T) {
		t.Parallel()

		dest := filepath.Join(t.TempDir(), "stuff.txt")

		writeFile(t, dest, "one\ntwo\n")

		stdout, _, err := runXtee(t, strings.NewReader("two\nthree\n"), "--preview", "--unique", "--append", dest)
		if err != nil {
			t.Fatalf("run: %v", err)
		}

		if stdout != "three\n" {
			t.Errorf("stdout = %q, want only lines that would be appended", stdout)
		}

		if got := readFile(t, dest); got != "one\ntwo\n" {
			t.Errorf("file = %q, want untouched", got)
		}
	})
}

func TestQuietSuppressesStdout(t *testing.T) {
	t.Parallel()

	dest := filepath.Join(t.TempDir(), "out.txt")

	stdout, _, err := runXtee(t, strings.NewReader("a\n"), "--quiet", dest)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}

	if got := readFile(t, dest); got != "a\n" {
		t.Errorf("file = %q, want %q", got, "a\\n")
	}
}

// TestFlushError verifies write failures surface as a non-zero exit status
// instead of silently producing truncated output (/dev/full rejects writes).
func TestFlushError(t *testing.T) {
	t.Parallel()

	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("/dev/full not available")
	}

	if _, _, err := runXtee(t, strings.NewReader("a\n"), "--quiet", "/dev/full"); err == nil {
		t.Error("run against /dev/full: want non-zero exit status")
	}
}

func TestLongLineLimit(t *testing.T) {
	t.Parallel()

	t.Run("TwoMegabyteLineAccepted", func(t *testing.T) {
		t.Parallel()

		line := strings.Repeat("a", 2*1024*1024)
		dest := filepath.Join(t.TempDir(), "out.txt")

		if _, _, err := runXtee(t, strings.NewReader(line+"\n"), "--quiet", dest); err != nil {
			t.Fatalf("run: %v", err)
		}

		if got := readFile(t, dest); got != line+"\n" {
			t.Errorf("file = %d bytes, want %d", len(got), len(line)+1)
		}
	})

	t.Run("OverLimitLineRejected", func(t *testing.T) {
		t.Parallel()

		line := strings.Repeat("a", 8*1024*1024+1)
		dest := filepath.Join(t.TempDir(), "out.txt")

		if _, _, err := runXtee(t, strings.NewReader(line+"\n"), "--quiet", dest); err == nil {
			t.Error("run with over-limit line: want non-zero exit status")
		}
	})
}

func TestNoStdin(t *testing.T) {
	t.Parallel()

	dest := filepath.Join(t.TempDir(), "out.txt")

	_, stderr, err := runXtee(t, nil, dest)
	if err == nil {
		t.Fatal("run without piped stdin: want non-zero exit status")
	}

	if !strings.Contains(stderr, "stdin stream expected") {
		t.Errorf("stderr = %q, want %q to be mentioned", stderr, "stdin stream expected")
	}
}

func TestShorthandFlags(t *testing.T) {
	t.Parallel()

	dest := filepath.Join(t.TempDir(), "out.txt")

	if _, _, err := runXtee(t, strings.NewReader("a\n"), "-m", "-s", dest); err != nil {
		t.Fatalf("run with -m -s: %v", err)
	}
}
