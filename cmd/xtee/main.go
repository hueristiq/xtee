package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	hqgologger "github.com/hueristiq/hq-go-logger"
	hqgologgerformatter "github.com/hueristiq/hq-go-logger/formatter"
	hqgologgerlevels "github.com/hueristiq/hq-go-logger/levels"
	"github.com/hueristiq/xtee/internal/configuration"
	"github.com/hueristiq/xtee/internal/input"
	"github.com/logrusorgru/aurora/v4"
	"github.com/spf13/pflag"
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

var (
	o *options

	au = aurora.New(aurora.WithColors(true))
)

func init() {
	o = &options{}

	pflag.BoolVar(&o.soak, "soak", false, "")
	pflag.BoolVar(&o.append, "append", false, "")
	pflag.BoolVar(&o.unique, "unique", false, "")
	pflag.BoolVar(&o.preview, "preview", false, "")
	pflag.BoolVarP(&o.quiet, "quiet", "q", false, "")
	pflag.BoolVar(&o.monochrome, "monochrome", false, "")
	pflag.BoolVar(&o.silent, "silent", false, "")
	pflag.BoolVarP(&o.verbose, "verbose", "v", false, "")

	pflag.Usage = func() {
		hqgologger.Info(configuration.BANNER(au), hqgologger.WithLabel(""))

		h := "USAGE:\n"
		h += fmt.Sprintf(" %s [OPTION]... <FILE>\n", configuration.NAME)

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

		hqgologger.Info(h, hqgologger.WithLabel(""))
		hqgologger.Print("")
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

	au = aurora.New(aurora.WithColors(!o.monochrome))
}

func main() {
	if !input.HasStdin() {
		hqgologger.Fatal("stdin stream expected!")
	}

	destination := pflag.Arg(0)

	if !o.preview && destination == "" {
		hqgologger.Fatal("file expected!")
	}

	var fileWriter *os.File

	var err error

	if !o.preview {
		fileWriter, err = getWriter(destination, o.append)
		if err != nil {
			hqgologger.Fatal(err.Error())
		}

		defer fileWriter.Close()
	}

	var existingLines map[string]struct{}

	if o.unique && o.append && !o.preview {
		existingLines = make(map[string]struct{})

		if err := loadExistingLines(destination, existingLines); err != nil && !os.IsNotExist(err) {
			hqgologger.Fatal(err.Error())
		}
	}

	if o.soak {
		if err := processBufferedInput(o, existingLines, fileWriter); err != nil {
			hqgologger.Fatal(err.Error())
		}
	} else {
		if err := processStreamedInput(o, existingLines, fileWriter); err != nil {
			hqgologger.Fatal(err.Error())
		}
	}
}

func loadExistingLines(file string, lines map[string]struct{}) (err error) {
	f, err := os.Open(file)
	if err != nil {
		return
	}

	defer f.Close()

	scanner := bufio.NewScanner(f)

	buf := make([]byte, 0, 64*1024)

	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		lines[scanner.Text()] = struct{}{}
	}

	if err = scanner.Err(); err != nil {
		return
	}

	return
}

func getWriter(path string, appendToFile bool) (file *os.File, err error) {
	if err = os.MkdirAll(filepath.Dir(path), os.ModePerm); err != nil {
		return
	}

	flags := os.O_CREATE | os.O_WRONLY
	if appendToFile {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}

	file, err = os.OpenFile(path, flags, 0o644)
	if err != nil {
		return
	}

	return
}

func processBufferedInput(o *options, existingLines map[string]struct{}, writer *os.File) (err error) {
	var bufWriter *bufio.Writer

	if !o.preview && writer != nil {
		bufWriter = bufio.NewWriterSize(writer, 64*1024)

		defer bufWriter.Flush()
	}

	seen := make(map[string]struct{}, len(existingLines))

	for k := range existingLines {
		seen[k] = struct{}{}
	}

	scanner := bufio.NewScanner(os.Stdin)

	buf := make([]byte, 0, 64*1024)

	scanner.Buffer(buf, 1024*1024)

	lines := make([]string, 0, 1024)

	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if err = scanner.Err(); err != nil {
		return
	}

	numWorkers := runtime.NumCPU()

	if numWorkers < 1 {
		numWorkers = 1
	}

	if len(lines) < numWorkers {
		numWorkers = 1
	}

	var wg sync.WaitGroup

	chunkSize := (len(lines) + numWorkers - 1) / numWorkers
	errChan := make(chan error, numWorkers)
	seenMutex := &sync.Mutex{}

	for i := range numWorkers {
		start := i * chunkSize

		end := start + chunkSize

		if end > len(lines) {
			end = len(lines)
		}

		if start >= end {
			continue
		}

		wg.Add(1)

		go func(chunk []string) {
			defer wg.Done()

			for _, line := range chunk {
				if err = processLine(o, line, seen, seenMutex, bufWriter); err != nil {
					errChan <- err

					return
				}
			}
		}(lines[start:end])
	}

	go func() {
		wg.Wait()

		close(errChan)
	}()

	for err = range errChan {
		if err != nil {
			return
		}
	}

	return
}

func processStreamedInput(o *options, existingLines map[string]struct{}, writer *os.File) (err error) {
	var bufWriter *bufio.Writer

	if !o.preview && writer != nil {
		bufWriter = bufio.NewWriterSize(writer, 64*1024)

		defer bufWriter.Flush()
	}

	seen := make(map[string]struct{}, len(existingLines))

	for k := range existingLines {
		seen[k] = struct{}{}
	}

	seenMutex := &sync.Mutex{}

	scanner := bufio.NewScanner(os.Stdin)

	buf := make([]byte, 0, 64*1024)

	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if err = processLine(o, line, seen, seenMutex, bufWriter); err != nil {
			return
		}
	}

	if err = scanner.Err(); err != nil {
		return
	}

	return
}

func processLine(o *options, line string, seen map[string]struct{}, mu *sync.Mutex, writer *bufio.Writer) (err error) {
	if o.unique {
		mu.Lock()

		if _, exists := seen[line]; exists {
			mu.Unlock()

			return
		}

		seen[line] = struct{}{}

		mu.Unlock()
	}

	if !o.quiet {
		hqgologger.Print(line)
	}

	if !o.preview && writer != nil {
		if _, err = writer.WriteString(line); err != nil {
			return
		}

		if err = writer.WriteByte('\n'); err != nil {
			return
		}
	}

	return
}
