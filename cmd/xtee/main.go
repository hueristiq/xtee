package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"hash/maphash"
	"io"
	"os"
	"path/filepath"

	hqgologger "github.com/hueristiq/hq-lib-logger-go"
	hqgologgerformatter "github.com/hueristiq/hq-lib-logger-go/formatter"
	hqgologgerlevels "github.com/hueristiq/hq-lib-logger-go/levels"
	"github.com/logrusorgru/aurora/v4"
	"github.com/spf13/pflag"

	"github.com/hueristiq/xtee/internal/configuration"
	"github.com/hueristiq/xtee/internal/input"
)

const (
	ioBufferSize  = 256 * 1024
	maxLineLength = 8 * 1024 * 1024
)

type options struct {
	soak       bool
	append     bool
	unique     bool
	preview    bool
	quiet      bool
	monochrome bool
	silent     bool
	verbose    bool
}

type stats struct {
	read    int
	written int
	skipped int
}

func main() {
	o := parseFlags()

	if !input.HasStdin() {
		hqgologger.Fatal("stdin stream expected!")
	}

	if pflag.NArg() > 1 {
		hqgologger.Fatal("multiple files are not supported; provide exactly one destination!")
	}

	if o.preview && o.quiet {
		hqgologger.Fatal("--preview and --quiet cannot be used together!")
	}

	destination := pflag.Arg(0)

	if !o.preview && destination == "" {
		hqgologger.Fatal("file expected!")
	}

	if err := run(o, destination, os.Stdin); err != nil {
		hqgologger.Fatal(err.Error())
	}
}

func parseFlags() (o *options) {
	o = &options{}

	pflag.BoolVar(&o.soak, "soak", false, "")
	pflag.BoolVar(&o.append, "append", false, "")
	pflag.BoolVar(&o.unique, "unique", false, "")
	pflag.BoolVar(&o.preview, "preview", false, "")
	pflag.BoolVarP(&o.quiet, "quiet", "q", false, "")
	pflag.BoolVarP(&o.monochrome, "monochrome", "m", false, "")
	pflag.BoolVarP(&o.silent, "silent", "s", false, "")
	pflag.BoolVarP(&o.verbose, "verbose", "v", false, "")

	pflag.Usage = func() {
		au := aurora.New(aurora.WithColors(!o.monochrome))

		hqgologger.Print(configuration.Banner(au), hqgologger.WithoutTimestamp(), hqgologger.WithoutLabel())

		h := "USAGE:\n"
		h += " " + configuration.Name + " [OPTION]... <FILE>\n"

		h += "\nINPUT:\n"
		h += "     --soak bool          buffer input before processing\n"

		h += "\nOUTPUT:\n"
		h += "     --append bool        append lines\n"
		h += "     --unique bool        unique lines\n"
		h += "     --preview bool       preview lines\n"
		h += " -q, --quiet bool         suppress stdout\n"
		h += " -m, --monochrome bool    disable colored console output\n"
		h += " -s, --silent bool        disable logging output, only results\n"
		h += " -v, --verbose bool       enable detailed debug logging output\n"

		hqgologger.Print(h, hqgologger.WithoutTimestamp(), hqgologger.WithoutLabel())
	}

	pflag.Parse()

	hqgologger.DefaultLogger.SetFormatter(hqgologgerformatter.NewConsoleFormatter(&hqgologgerformatter.ConsoleFormatterConfiguration{
		Colorize: !o.monochrome,
	}))

	_ = hqgologger.DefaultLogger.SetLevel(hqgologgerlevels.LevelInfo) // level constants are valid by construction

	if o.verbose {
		_ = hqgologger.DefaultLogger.SetLevel(hqgologgerlevels.LevelDebug) // level constants are valid by construction
	}

	if o.silent {
		_ = hqgologger.DefaultLogger.SetLevel(hqgologgerlevels.LevelSilent) // level constants are valid by construction
	}

	return o
}

func run(o *options, destination string, stdin io.Reader) (err error) {
	if o.soak {
		return runSoaked(o, destination, stdin)
	}

	return runStreamed(o, destination, stdin)
}

func runStreamed(o *options, destination string, stdin io.Reader) (err error) {
	seen, err := loadSeen(o, destination)
	if err != nil {
		return err
	}

	var file *os.File

	if !o.preview {
		file, err = getWriter(destination, o.append)
		if err != nil {
			return err
		}

		hqgologger.Debug(fmt.Sprintf("destination %q opened", destination))
	}

	var fileWriter *bufio.Writer

	if file != nil {
		fileWriter = bufio.NewWriterSize(file, ioBufferSize)
	}

	stdoutWriter := newStdoutWriter(o)

	defer func() {
		err = errors.Join(err, teardown(stdoutWriter, fileWriter, file))
	}()

	st := &stats{}

	scanner := bufio.NewScanner(bufio.NewReaderSize(stdin, ioBufferSize))

	scanner.Buffer(make([]byte, 0, 64*1024), maxLineLength)

	for scanner.Scan() {
		if err := emit(o, scanner.Bytes(), seen, stdoutWriter, fileWriter, st); err != nil {
			return err
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanning input: %w", err)
	}

	logStats(st)

	return nil
}

func runSoaked(o *options, destination string, stdin io.Reader) (err error) {
	data, starts, err := soakInput(stdin)
	if err != nil {
		return err
	}

	hqgologger.Debug(fmt.Sprintf("soaked %d bytes in %d lines", len(data), len(starts)))

	seen, err := loadSeen(o, destination)
	if err != nil {
		return err
	}

	var file *os.File

	if !o.preview {
		file, err = getWriter(destination, o.append)
		if err != nil {
			return err
		}

		hqgologger.Debug(fmt.Sprintf("destination %q opened", destination))
	}

	var fileWriter *bufio.Writer

	if file != nil {
		fileWriter = bufio.NewWriterSize(file, ioBufferSize)
	}

	stdoutWriter := newStdoutWriter(o)

	defer func() {
		err = errors.Join(err, teardown(stdoutWriter, fileWriter, file))
	}()

	st := &stats{}

	for i, start := range starts {
		end := len(data)

		if i+1 < len(starts) {
			end = starts[i+1]
		}

		line := data[start:end]

		if len(line) > 0 && line[len(line)-1] == '\n' {
			line = line[:len(line)-1]
		}

		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}

		if len(line) > maxLineLength {
			return fmt.Errorf("line %d exceeds the maximum line length of %d bytes", i+1, maxLineLength)
		}

		if err := emit(o, line, seen, stdoutWriter, fileWriter, st); err != nil {
			return err
		}
	}

	logStats(st)

	return nil
}

func soakInput(r io.Reader) (data []byte, starts []int, err error) {
	data, err = io.ReadAll(r)
	if err != nil {
		return nil, nil, fmt.Errorf("soaking input: %w", err)
	}

	starts = make([]int, 0, 1024)

	for offset := 0; offset < len(data); {
		starts = append(starts, offset)

		idx := bytes.IndexByte(data[offset:], '\n')
		if idx < 0 {
			break
		}

		offset += idx + 1
	}

	return data, starts, nil
}

func emit(o *options, line []byte, seen *lineSet, stdoutWriter, fileWriter *bufio.Writer, st *stats) (err error) {
	st.read++

	if o.unique {
		if seen.contains(line) {
			st.skipped++

			return nil
		}

		seen.add(line)
	}

	if stdoutWriter != nil {
		if _, err := stdoutWriter.Write(line); err != nil {
			return fmt.Errorf("writing line to stdout: %w", err)
		}

		if err := stdoutWriter.WriteByte('\n'); err != nil {
			return fmt.Errorf("writing line terminator to stdout: %w", err)
		}
	}

	if fileWriter != nil {
		if _, err := fileWriter.Write(line); err != nil {
			return fmt.Errorf("writing line: %w", err)
		}

		if err := fileWriter.WriteByte('\n'); err != nil {
			return fmt.Errorf("writing line terminator: %w", err)
		}
	}

	st.written++

	return nil
}

func newStdoutWriter(o *options) (stdout *bufio.Writer) {
	if o.quiet {
		return nil
	}

	return bufio.NewWriterSize(os.Stdout, ioBufferSize)
}

func logStats(st *stats) {
	hqgologger.Debug(fmt.Sprintf("processed %d lines: %d written, %d duplicates skipped", st.read, st.written, st.skipped))
}

func teardown(stdoutWriter, fileWriter *bufio.Writer, file *os.File) (err error) {
	if stdoutWriter != nil {
		if ferr := stdoutWriter.Flush(); ferr != nil {
			err = errors.Join(err, fmt.Errorf("flushing stdout: %w", ferr))
		}
	}

	if fileWriter != nil {
		if ferr := fileWriter.Flush(); ferr != nil {
			err = errors.Join(err, fmt.Errorf("flushing output: %w", ferr))
		}
	}

	if file != nil {
		if cerr := file.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("closing destination: %w", cerr))
		}
	}

	return err
}

func loadSeen(o *options, destination string) (seen *lineSet, err error) {
	seen = newLineSet()

	if !o.unique || !o.append || destination == "" {
		return seen, nil
	}

	if err := loadExistingLines(destination, seen); err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	return seen, nil
}

func loadExistingLines(file string, lines *lineSet) (err error) {
	f, err := os.Open(file) //nolint:gosec // the destination is user-provided by design
	if err != nil {
		return fmt.Errorf("opening %q: %w", file, err)
	}

	defer func() {
		err = errors.Join(err, f.Close())
	}()

	scanner := bufio.NewScanner(f)

	scanner.Buffer(make([]byte, 0, 64*1024), maxLineLength)

	for scanner.Scan() {
		lines.add(scanner.Bytes())
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanning %q: %w", file, err)
	}

	return nil
}

func getWriter(destination string, appendToFile bool) (file *os.File, err error) {
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return nil, fmt.Errorf("creating parent directories for %q: %w", destination, err)
	}

	flags := os.O_CREATE | os.O_RDWR

	if appendToFile {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}

	file, err = os.OpenFile(destination, flags, 0o644) //nolint:gosec // destination files are user data, conventionally world-readable like tee
	if err != nil {
		return nil, fmt.Errorf("opening %q: %w", destination, err)
	}

	if appendToFile {
		if err := ensureTrailingNewline(file); err != nil {
			_ = file.Close()

			return nil, fmt.Errorf("preparing %q for append: %w", destination, err)
		}
	}

	return file, nil
}

// ensureTrailingNewline appends a newline when the file is non-empty and its
// last byte is not one, so the first appended line does not glue onto a
// partial final line.
func ensureTrailingNewline(file *os.File) (err error) {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stating file: %w", err)
	}

	if info.Size() == 0 {
		return nil
	}

	var last [1]byte

	if _, err := file.ReadAt(last[:], info.Size()-1); err != nil {
		return fmt.Errorf("reading last byte: %w", err)
	}

	if last[0] == '\n' {
		return nil
	}

	if _, err := file.Write([]byte{'\n'}); err != nil {
		return fmt.Errorf("appending newline: %w", err)
	}

	return nil
}

// lineSet is a memory-efficient set of line hashes. It stores one 64-bit hash
// per unique line instead of the lines themselves, so memory usage is bounded
// by the number of unique lines rather than their combined size. The trade-off
// is a theoretically possible (astronomically unlikely) hash collision.
type lineSet struct {
	table []uint64
	size  int
	mask  uint64
	seed  maphash.Seed
}

func newLineSet() (set *lineSet) {
	set = &lineSet{
		table: make([]uint64, 16),
		seed:  maphash.MakeSeed(),
	}

	set.mask = uint64(len(set.table) - 1) //nolint:gosec // table is a positive power of two; no overflow possible

	return set
}

func (set *lineSet) hash(line []byte) (hash uint64) {
	hash = maphash.Bytes(set.seed, line)

	// Zero is the empty-slot sentinel; remap a zero hash to keep it valid.
	if hash == 0 {
		hash = 1
	}

	return hash
}

func (set *lineSet) contains(line []byte) bool {
	hash := set.hash(line)

	for i := hash & set.mask; set.table[i] != 0; i = (i + 1) & set.mask {
		if set.table[i] == hash {
			return true
		}
	}

	return false
}

func (set *lineSet) add(line []byte) {
	if set.contains(line) {
		return
	}

	if set.size*2 >= len(set.table) {
		set.grow()
	}

	hash := set.hash(line)
	i := hash & set.mask

	for set.table[i] != 0 {
		i = (i + 1) & set.mask
	}

	set.table[i] = hash
	set.size++
}

func (set *lineSet) grow() {
	old := set.table

	set.table = make([]uint64, len(old)*2)
	set.mask = uint64(len(set.table) - 1) //nolint:gosec // table is a positive power of two; no overflow possible
	set.size = 0

	for _, hash := range old {
		if hash == 0 {
			continue
		}

		i := hash & set.mask

		for set.table[i] != 0 {
			i = (i + 1) & set.mask
		}

		set.table[i] = hash
		set.size++
	}
}
