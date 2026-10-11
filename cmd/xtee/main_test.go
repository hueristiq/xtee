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
	"time"
)

const lineA = "a\n"

var xteePath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "xtee-test")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create temp dir: %v\n", err)
		os.Exit(1)
	}

	xteePath = filepath.Join(dir, "xtee")

	if !buildBinary(xteePath) {
		_ = os.RemoveAll(dir) // test cleanup is best-effort

		os.Exit(1)
	}

	code := m.Run()

	_ = os.RemoveAll(dir) // test cleanup is best-effort

	os.Exit(code)
}

func buildBinary(path string) (ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)

	defer cancel()

	build := exec.CommandContext(ctx, "go", "build", "-race", "-o", path, ".") //nolint:gosec // tests build the binary under test

	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "race build failed (%v), falling back to plain build:\n%s\n", err, out)

		build = exec.CommandContext(ctx, "go", "build", "-o", path, ".") //nolint:gosec // tests build the binary under test

		if out, err := build.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "build: %v\n%s\n", err, out)

			return false
		}
	}

	return true
}

func runXtee(t *testing.T, stdin io.Reader, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), xteePath, args...) //nolint:gosec // tests execute the freshly built binary by design

	cmd.Stdin = stdin

	var outBuf, errBuf bytes.Buffer

	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err = cmd.Run()

	return outBuf.String(), errBuf.String(), err
}

func readFile(t *testing.T, path string) (content string) {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // reads test-fixture paths only
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	return string(data)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
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

	if got := readFile(t, dest); got != lineA {
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

	stdout, stderr, err := runXtee(t, strings.NewReader("a\n"), "-m", "-s", dest)
	if err != nil {
		t.Fatalf("run with -m -s: %v", err)
	}

	if stdout != lineA {
		t.Errorf("stdout = %q, want results despite -s", stdout)
	}

	if stderr != "" {
		t.Errorf("stderr = %q, want logs suppressed by -s", stderr)
	}

	if got := readFile(t, dest); got != lineA {
		t.Errorf("file = %q, want %q", got, "a\\n")
	}
}

func TestSoakOverLimitLineRejected(t *testing.T) {
	t.Parallel()

	line := strings.Repeat("a", 8*1024*1024+1)
	dest := filepath.Join(t.TempDir(), "out.txt")

	if _, _, err := runXtee(t, strings.NewReader(line+"\n"), "--soak", "--quiet", dest); err == nil {
		t.Error("soak run with over-limit line: want non-zero exit status")
	}
}

func TestAppendMissingTrailingNewline(t *testing.T) {
	t.Parallel()

	t.Run("Streamed", func(t *testing.T) {
		t.Parallel()

		dest := filepath.Join(t.TempDir(), "stuff.txt")

		writeFile(t, dest, "one\ntwo")

		if _, _, err := runXtee(t, strings.NewReader("three\n"), "--append", "--quiet", dest); err != nil {
			t.Fatalf("run: %v", err)
		}

		if got := readFile(t, dest); got != "one\ntwo\nthree\n" {
			t.Errorf("file = %q, want %q", got, "one\\ntwo\\nthree\\n")
		}
	})

	t.Run("Soak", func(t *testing.T) {
		t.Parallel()

		dest := filepath.Join(t.TempDir(), "stuff.txt")

		writeFile(t, dest, "one\ntwo")

		if _, _, err := runXtee(t, strings.NewReader("three\n"), "--soak", "--append", "--quiet", dest); err != nil {
			t.Fatalf("run: %v", err)
		}

		if got := readFile(t, dest); got != "one\ntwo\nthree\n" {
			t.Errorf("file = %q, want %q", got, "one\\ntwo\\nthree\\n")
		}
	})
}

func TestCRLFInput(t *testing.T) {
	t.Parallel()

	t.Run("Streamed", func(t *testing.T) {
		t.Parallel()

		dest := filepath.Join(t.TempDir(), "out.txt")

		if _, _, err := runXtee(t, strings.NewReader("a\r\nb\r\n"), "--quiet", dest); err != nil {
			t.Fatalf("run: %v", err)
		}

		if got := readFile(t, dest); got != "a\nb\n" {
			t.Errorf("file = %q, want %q", got, "a\\nb\\n")
		}
	})

	t.Run("Soak", func(t *testing.T) {
		t.Parallel()

		dest := filepath.Join(t.TempDir(), "out.txt")

		if _, _, err := runXtee(t, strings.NewReader("a\r\nb\r\n"), "--soak", "--quiet", dest); err != nil {
			t.Fatalf("run: %v", err)
		}

		if got := readFile(t, dest); got != "a\nb\n" {
			t.Errorf("file = %q, want %q", got, "a\\nb\\n")
		}
	})
}

func TestEmptyInput(t *testing.T) {
	t.Parallel()

	t.Run("Streamed", func(t *testing.T) {
		t.Parallel()

		dest := filepath.Join(t.TempDir(), "out.txt")

		if _, _, err := runXtee(t, strings.NewReader(""), "--quiet", dest); err != nil {
			t.Fatalf("run: %v", err)
		}

		if got := readFile(t, dest); got != "" {
			t.Errorf("file = %q, want empty", got)
		}
	})

	t.Run("Soak", func(t *testing.T) {
		t.Parallel()

		dest := filepath.Join(t.TempDir(), "out.txt")

		if _, _, err := runXtee(t, strings.NewReader(""), "--soak", "--quiet", dest); err != nil {
			t.Fatalf("run: %v", err)
		}

		if got := readFile(t, dest); got != "" {
			t.Errorf("file = %q, want empty", got)
		}
	})
}

func TestEmptyLines(t *testing.T) {
	t.Parallel()

	dest := filepath.Join(t.TempDir(), "out.txt")

	if _, _, err := runXtee(t, strings.NewReader("\n\n"), "--quiet", dest); err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := readFile(t, dest); got != "\n\n" {
		t.Errorf("file = %q, want two empty lines", got)
	}
}

func TestExtraPositionalArgsRejected(t *testing.T) {
	t.Parallel()

	dest := filepath.Join(t.TempDir(), "out.txt")

	if _, _, err := runXtee(t, strings.NewReader("a\n"), "--quiet", dest, dest+".other"); err == nil {
		t.Error("run with two destinations: want non-zero exit status")
	}
}

func TestPreviewQuietRejected(t *testing.T) {
	t.Parallel()

	dest := filepath.Join(t.TempDir(), "out.txt")

	if _, _, err := runXtee(t, strings.NewReader("a\n"), "--preview", "--quiet", dest); err == nil {
		t.Error("run with --preview --quiet: want non-zero exit status")
	}
}

func TestHelpGoesToStdout(t *testing.T) {
	t.Parallel()

	stdout, _, err := runXtee(t, strings.NewReader(""), "-h")
	if err != nil {
		t.Fatalf("run with -h: %v", err)
	}

	if !strings.Contains(stdout, "USAGE:") {
		t.Errorf("stdout = %q, want usage text", stdout)
	}
}
