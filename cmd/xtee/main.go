package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	hqgologger "github.com/hueristiq/hq-go-logger"
	hqgologgerformatter "github.com/hueristiq/hq-go-logger/formatter"
	hqgologgerlevels "github.com/hueristiq/hq-go-logger/levels"
	"github.com/hueristiq/xtee/internal/configuration"
	"github.com/hueristiq/xtee/internal/input"
	"github.com/logrusorgru/aurora/v4"
	"github.com/spf13/pflag"
)

var (
	soak         bool
	appendLines  bool
	uniqueLines  bool
	previewLines bool
	quiet        bool
	monochrome   bool
	silent       bool
	verbose      bool

	au = aurora.New(aurora.WithColors(true))
)

func init() {
	pflag.BoolVar(&soak, "soak", false, "")
	pflag.BoolVar(&appendLines, "append", false, "")
	pflag.BoolVar(&uniqueLines, "unique", false, "")
	pflag.BoolVar(&previewLines, "preview", false, "")
	pflag.BoolVarP(&quiet, "quiet", "q", false, "")
	pflag.BoolVar(&monochrome, "monochrome", false, "")
	pflag.BoolVar(&silent, "silent", false, "")
	pflag.BoolVarP(&verbose, "verbose", "v", false, "")

	pflag.Usage = func() {
		hqgologger.Info().Label("").Msg(configuration.BANNER(au))

		h := "USAGE:\n"
		h += fmt.Sprintf(" %s [OPTION]... <FILE>\n", configuration.NAME)

		h += "\nINPUT:\n"
		h += "     --soak bool          buffer input before processing\n"

		h += "\nOUTPUT:\n"
		h += "     --append bool        append lines\n"
		h += "     --unique bool        unique lines\n"
		h += "     --preview bool       preview lines\n"
		h += " -q, --quiet bool         suppress stdout\n"
		h += " -m, --monochrome bool    stdout in monochrome\n"
		h += " -s, --silent bool        stdout in silent mode\n"
		h += " -v, --verbose bool       stdout in verbose mode\n"

		hqgologger.Info().Label("").Msg(h)
		hqgologger.Print().Msg("")
	}

	pflag.Parse()

	hqgologger.DefaultLogger.SetFormatter(hqgologgerformatter.NewConsoleFormatter(&hqgologgerformatter.ConsoleFormatterConfiguration{
		Colorize: !monochrome,
	}))

	if silent {
		hqgologger.DefaultLogger.SetLevel(hqgologgerlevels.LevelSilent)
	}

	if verbose {
		hqgologger.DefaultLogger.SetLevel(hqgologgerlevels.LevelDebug)
	}

	au = aurora.New(aurora.WithColors(!monochrome))
}

func main() {
	hqgologger.Info().Label("").Msg(configuration.BANNER(au))

	if !input.HasStdin() {
		hqgologger.Fatal().Msg("stdin stream expected!")
	}

	destination := pflag.Arg(0)

	if !previewLines && destination == "" {
		hqgologger.Fatal().Msg("file expected!")
	}

	var err error

	var unique *sync.Map

	if uniqueLines {
		unique = &sync.Map{}

		if err = loadExistingLines(destination, unique); err != nil && !os.IsNotExist(err) {
			hqgologger.Fatal().Msg(err.Error())
		}
	}

	var writer io.WriteCloser

	if !previewLines {
		writer, err = getWriteCloser(destination, appendLines)
		if err != nil {
			hqgologger.Fatal().Msg(err.Error())
		}

		defer writer.Close()
	}

	if soak {
		if err = processBufferedInput(unique, writer); err != nil {
			hqgologger.Fatal().Msg(err.Error())
		}
	} else {
		if err = processStreamedInput(unique, writer); err != nil {
			hqgologger.Fatal().Msg(err.Error())
		}
	}
}

func loadExistingLines(file string, lines *sync.Map) (err error) {
	f, err := os.Open(file)
	if err != nil {
		return
	}

	defer f.Close()

	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		lines.Store(scanner.Text(), struct{}{})
	}

	if err = scanner.Err(); err != nil {
		return
	}

	return
}

func getWriteCloser(file string, appendToFile bool) (writer io.WriteCloser, err error) {
	if err = os.MkdirAll(filepath.Dir(file), os.ModePerm); err != nil {
		return
	}

	flags := os.O_CREATE | os.O_WRONLY

	if appendToFile {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}

	if writer, err = os.OpenFile(file, flags, 0o644); err != nil {
		return
	}

	return
}

func processBufferedInput(uniqueLines *sync.Map, writer io.WriteCloser) (err error) {
	lines := []string{}

	scanner := bufio.NewScanner(os.Stdin)

	for scanner.Scan() {
		line := scanner.Text()

		lines = append(lines, line)
	}

	if err = scanner.Err(); err != nil {
		return
	}

	for _, line := range lines {
		if err = processLine(line, uniqueLines, writer); err != nil {
			return
		}
	}

	return
}

func processStreamedInput(uniqueLines *sync.Map, writer io.WriteCloser) (err error) {
	scanner := bufio.NewScanner(os.Stdin)

	for scanner.Scan() {
		line := scanner.Text()

		if err = processLine(line, uniqueLines, writer); err != nil {
			return
		}
	}

	if err = scanner.Err(); err != nil {
		return
	}

	return
}

func processLine(line string, unique *sync.Map, writer io.WriteCloser) (err error) {
	if uniqueLines {
		if _, loaded := unique.LoadOrStore(line, struct{}{}); loaded {
			return
		}
	}

	if !quiet {
		hqgologger.Print().Msg(line)
	}

	if !previewLines && writer != nil {
		if _, err = fmt.Fprintln(writer, line); err != nil {
			return
		}
	}

	return
}
