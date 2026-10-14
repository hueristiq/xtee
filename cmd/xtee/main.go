package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"hash/maphash"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	hqgologger "github.com/hueristiq/hq-lib-logger-go"
	hqgologgerformatter "github.com/hueristiq/hq-lib-logger-go/formatter"
	hqgologgerlevels "github.com/hueristiq/hq-lib-logger-go/levels"
	"github.com/logrusorgru/aurora/v4"
	"github.com/spf13/pflag"

	"github.com/hueristiq/xtee/internal/configuration"
	"github.com/hueristiq/xtee/internal/input"
)

type output struct {
	stdout io.Writer
	file   *os.File
	fileW  *bufio.Writer
	buf    []byte

	stdoutErr error
}

type lineSet struct {
	table []uint64
	size  int
	mask  uint64
	seed  maphash.Seed
}

const (
	ioBufferSize  = 256 * 1024
	maxLineLength = 8 * 1024 * 1024
)

var (
	soak         bool
	appendToFile bool
	uniqueLines  bool
	preview      bool
	quiet        bool
	monochrome   bool
	silent       bool

	au = aurora.New(aurora.WithColors(true))
)

func init() {
	pflag.BoolVar(&soak, "soak", false, "")
	pflag.BoolVarP(&appendToFile, "append", "a", false, "")
	pflag.BoolVarP(&uniqueLines, "unique", "u", false, "")
	pflag.BoolVar(&preview, "preview", false, "")
	pflag.BoolVarP(&quiet, "quiet", "q", false, "")
	pflag.BoolVarP(&monochrome, "monochrome", "m", false, "")
	pflag.BoolVarP(&silent, "silent", "s", false, "")

	pflag.CommandLine.SetNormalizeFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		if name == "uniq" {
			name = "unique"
		}

		return pflag.NormalizedName(name)
	})

	pflag.Usage = func() {
		hqgologger.Print(configuration.Banner(au), hqgologger.WithoutTimestamp(), hqgologger.WithoutLabel())

		help := "USAGE:\n"
		help += " " + configuration.Name + " [OPTION]... <FILE>\n"

		help += "\nINPUT:\n"
		help += "     --soak bool          buffer input before processing\n"

		help += "\nOUTPUT:\n"
		help += " -a, --append bool        append lines\n"
		help += " -u, --unique bool        unique lines\n"
		help += "     --preview bool       preview lines\n"
		help += " -q, --quiet bool         suppress stdout\n"
		help += " -m, --monochrome bool    disable colored console output\n"
		help += " -s, --silent bool        disable logging output, only results\n"

		hqgologger.Print(help, hqgologger.WithoutTimestamp(), hqgologger.WithoutLabel())
		hqgologger.Print("", hqgologger.WithoutTimestamp(), hqgologger.WithoutLabel())
	}

	pflag.Parse()

	hqgologger.DefaultLogger.SetFormatter(hqgologgerformatter.NewConsoleFormatter(&hqgologgerformatter.ConsoleFormatterConfiguration{
		Colorize: !monochrome,
	}))

	if silent {
		if err := hqgologger.DefaultLogger.SetLevel(hqgologgerlevels.LevelSilent); err != nil {
			hqgologger.Fatal("failed setting logger level!", hqgologger.WithError(err))
		}
	}

	au = aurora.New(aurora.WithColors(!monochrome))
}

func main() {
	if !input.HasStdin() {
		hqgologger.Fatal("stdin stream expected!")
	}

	if pflag.NArg() > 1 {
		hqgologger.Fatal("multiple files are not supported; provide exactly one destination!")
	}

	if preview && quiet {
		hqgologger.Fatal("--preview and --quiet cannot be used together!")
	}

	destination := pflag.Arg(0)

	if !preview && destination == "" {
		hqgologger.Fatal("file expected!")
	}

	if !preview && !soak && destination != "" {
		if err := refuseInPlace(os.Stdin, destination); err != nil {
			hqgologger.Fatal(err.Error())
		}
	}

	signal.Ignore(syscall.SIGPIPE)

	if err := run(destination, os.Stdin); err != nil {
		hqgologger.Fatal(err.Error())
	}
}

func run(destination string, stdin io.Reader) (err error) {
	if soak {
		return runSoaked(destination, stdin)
	}

	return runStreamed(destination, stdin)
}

func runStreamed(destination string, stdin io.Reader) (err error) {
	seen, err := loadSeen(destination)
	if err != nil {
		return err
	}

	var file *os.File

	if !preview {
		file, err = getWriter(destination, appendToFile)
		if err != nil {
			return err
		}
	}

	out := newOutput(file)

	defer func() {
		err = errors.Join(err, out.close())
	}()

	scanner := bufio.NewScanner(bufio.NewReaderSize(stdin, ioBufferSize))

	scanner.Buffer(make([]byte, 0, 64*1024), maxLineLength+2)

	lineNumber := 1

	for scanner.Scan() {
		line := scanner.Bytes()

		if len(line) > maxLineLength {
			return errLineTooLong(lineNumber)
		}

		if err := emit(line, seen, out); err != nil {
			return err
		}

		lineNumber++
	}

	if errors.Is(scanner.Err(), bufio.ErrTooLong) {
		return fmt.Errorf("scanning input: %w", errLineTooLong(lineNumber))
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanning input: %w", err)
	}

	if out.stdoutErr != nil {
		return out.stdoutErr
	}

	return nil
}

func runSoaked(destination string, stdin io.Reader) (err error) {
	data, starts, err := soakInput(stdin)
	if err != nil {
		return err
	}

	seen, err := loadSeen(destination)
	if err != nil {
		return err
	}

	var file *os.File

	if !preview {
		file, err = getWriter(destination, appendToFile)
		if err != nil {
			return err
		}
	}

	out := newOutput(file)

	defer func() {
		err = errors.Join(err, out.close())
	}()

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
			return errLineTooLong(i + 1)
		}

		if err := emit(line, seen, out); err != nil {
			return err
		}
	}

	if out.stdoutErr != nil {
		return out.stdoutErr
	}

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

func emit(line []byte, seen *lineSet, out *output) (err error) {
	if uniqueLines && !seen.add(line) {
		return nil
	}

	return out.writeLine(line)
}

func errLineTooLong(lineNumber int) error {
	return fmt.Errorf("line %d exceeds the maximum line length of %d bytes", lineNumber, maxLineLength)
}

func newOutput(file *os.File) (out *output) {
	out = &output{
		buf: make([]byte, 0, 1024),
	}

	if !quiet {
		out.stdout = os.Stdout
	}

	if file != nil {
		out.file = file
		out.fileW = bufio.NewWriterSize(file, ioBufferSize)
	}

	return out
}

func (out *output) writeLine(line []byte) (err error) {
	out.buf = append(out.buf[:0], line...)
	out.buf = append(out.buf, '\n')

	if out.stdout != nil {
		if _, werr := out.stdout.Write(out.buf); werr != nil {
			out.stdout = nil

			if out.stdoutErr == nil {
				out.stdoutErr = fmt.Errorf("writing line to stdout: %w", werr)
			}
		}
	}

	if out.fileW != nil {
		if _, werr := out.fileW.Write(out.buf); werr != nil {
			return fmt.Errorf("writing line: %w", werr)
		}
	}

	return nil
}

func (out *output) close() (err error) {
	if out.fileW != nil {
		if ferr := out.fileW.Flush(); ferr != nil {
			err = errors.Join(err, fmt.Errorf("flushing destination: %w", ferr))
		}
	}

	if out.file != nil {
		err = errors.Join(err, closeFile(out.file))
	}

	return err
}

func closeFile(file *os.File) (err error) {
	if file == nil {
		return nil
	}

	if cerr := file.Close(); cerr != nil {
		err = fmt.Errorf("closing destination: %w", cerr)
	}

	return err
}

func refuseInPlace(stdin *os.File, destination string) (err error) {
	if !isSameRegularFile(stdin, destination) {
		return nil
	}

	return fmt.Errorf("refusing to read from and write to %q without --soak; use --soak for in-place processing", destination)
}

func isSameRegularFile(stdin *os.File, destination string) (same bool) {
	stdinInfo, err := stdin.Stat()
	if err != nil {
		return false
	}

	if !stdinInfo.Mode().IsRegular() {
		return false
	}

	destinationInfo, err := os.Stat(destination)
	if err != nil {
		return false
	}

	return os.SameFile(stdinInfo, destinationInfo)
}

func loadSeen(destination string) (seen *lineSet, err error) {
	seen = newLineSet()

	if !uniqueLines || !appendToFile || destination == "" {
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

	scanner.Buffer(make([]byte, 0, 64*1024), maxLineLength+2)

	lineNumber := 1

	for scanner.Scan() {
		line := scanner.Bytes()

		if len(line) > maxLineLength {
			return fmt.Errorf("scanning %q: %w", file, errLineTooLong(lineNumber))
		}

		lines.add(line)

		lineNumber++
	}

	if errors.Is(scanner.Err(), bufio.ErrTooLong) {
		return fmt.Errorf("scanning %q: %w", file, errLineTooLong(lineNumber))
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

	file, err = os.OpenFile(destination, flags, 0o666) //nolint:gosec // 0666 like tee; the process umask still applies
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

	if hash == 0 {
		hash = 1
	}

	return hash
}

func (set *lineSet) containsHash(hash uint64) (contains bool) {
	for i := hash & set.mask; set.table[i] != 0; i = (i + 1) & set.mask {
		if set.table[i] == hash {
			return true
		}
	}

	return false
}

func (set *lineSet) add(line []byte) (added bool) {
	hash := set.hash(line)

	if set.containsHash(hash) {
		return false
	}

	if set.size*2 >= len(set.table) {
		set.grow()
	}

	i := hash & set.mask

	for set.table[i] != 0 {
		i = (i + 1) & set.mask
	}

	set.table[i] = hash
	set.size++

	return true
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
