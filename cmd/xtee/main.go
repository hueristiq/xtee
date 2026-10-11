package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	hqgologger "github.com/hueristiq/hq-lib-logger-go"
	hqgologgerformatter "github.com/hueristiq/hq-lib-logger-go/formatter"
	hqgologgerlevels "github.com/hueristiq/hq-lib-logger-go/levels"
	"github.com/hueristiq/xtee/internal/configuration"
	"github.com/hueristiq/xtee/internal/input"
	"github.com/logrusorgru/aurora/v4"
	"github.com/spf13/pflag"
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

func main() {
	o := parseFlags()

	if !input.HasStdin() {
		hqgologger.Fatal("stdin stream expected!")
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

		hqgologger.Info(configuration.Banner(au), hqgologger.WithoutTimestamp(), hqgologger.WithoutLabel())

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

		hqgologger.Info(h, hqgologger.WithoutTimestamp(), hqgologger.WithoutLabel())
		hqgologger.Print("", hqgologger.WithoutTimestamp(), hqgologger.WithoutLabel())
	}

	pflag.Parse()

	hqgologger.DefaultLogger.SetFormatter(hqgologgerformatter.NewConsoleFormatter(&hqgologgerformatter.ConsoleFormatterConfiguration{
		Colorize: !o.monochrome,
	}))

	if o.silent {
		hqgologger.DefaultLogger.SetLevel(hqgologgerlevels.LevelSilent)
	}

	if o.verbose {
		hqgologger.DefaultLogger.SetLevel(hqgologgerlevels.LevelDebug)
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
	var file *os.File

	if !o.preview {
		file, err = getWriter(destination, o.append)
		if err != nil {
			return err
		}
	}

	var writer *bufio.Writer

	if file != nil {
		writer = bufio.NewWriterSize(file, ioBufferSize)
	}

	defer func() {
		err = errors.Join(err, teardown(writer, file))
	}()

	seen, err := loadSeen(o, destination)
	if err != nil {
		return err
	}

	scanner := bufio.NewScanner(bufio.NewReaderSize(stdin, ioBufferSize))

	scanner.Buffer(make([]byte, 0, 64*1024), maxLineLength)

	for scanner.Scan() {
		if err := emit(o, scanner.Bytes(), seen, writer); err != nil {
			return err
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanning input: %w", err)
	}

	return nil
}

func runSoaked(o *options, destination string, stdin io.Reader) (err error) {
	data, starts, err := soakInput(stdin)
	if err != nil {
		return err
	}

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
	}

	var writer *bufio.Writer

	if file != nil {
		writer = bufio.NewWriterSize(file, ioBufferSize)
	}

	defer func() {
		err = errors.Join(err, teardown(writer, file))
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

		if err := emit(o, line, seen, writer); err != nil {
			return err
		}
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

func emit(o *options, line []byte, seen map[string]struct{}, writer *bufio.Writer) (err error) {
	if o.unique {
		if _, exists := seen[string(line)]; exists {
			return nil
		}

		seen[string(line)] = struct{}{}
	}

	if !o.quiet {
		hqgologger.Print(string(line))
	}

	if writer != nil {
		if _, err := writer.Write(line); err != nil {
			return fmt.Errorf("writing line: %w", err)
		}

		if err := writer.WriteByte('\n'); err != nil {
			return fmt.Errorf("writing line terminator: %w", err)
		}
	}

	return nil
}

func teardown(writer *bufio.Writer, file *os.File) (err error) {
	if writer != nil {
		if ferr := writer.Flush(); ferr != nil {
			err = fmt.Errorf("flushing output: %w", ferr)
		}
	}

	if file != nil {
		if cerr := file.Close(); err == nil && cerr != nil {
			err = fmt.Errorf("closing destination: %w", cerr)
		}
	}

	return err
}

func loadSeen(o *options, destination string) (seen map[string]struct{}, err error) {
	seen = make(map[string]struct{})

	if !o.unique || !o.append || destination == "" {
		return seen, nil
	}

	if err := loadExistingLines(destination, seen); err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	return seen, nil
}

func loadExistingLines(file string, lines map[string]struct{}) (err error) {
	f, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("opening %q: %w", file, err)
	}

	defer f.Close()

	scanner := bufio.NewScanner(f)

	scanner.Buffer(make([]byte, 0, 64*1024), maxLineLength)

	for scanner.Scan() {
		lines[scanner.Text()] = struct{}{}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanning %q: %w", file, err)
	}

	return nil
}

func getWriter(destination string, appendToFile bool) (file *os.File, err error) {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return nil, fmt.Errorf("creating parent directories for %q: %w", destination, err)
	}

	flags := os.O_CREATE | os.O_WRONLY

	if appendToFile {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}

	file, err = os.OpenFile(destination, flags, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening %q: %w", destination, err)
	}

	return file, nil
}
