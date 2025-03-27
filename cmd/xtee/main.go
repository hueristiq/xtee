package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/hueristiq/xtee/internal/configuration"
	"github.com/hueristiq/xtee/internal/input"
	"github.com/logrusorgru/aurora/v4"
	"github.com/spf13/pflag"
	"go.source.hueristiq.com/logger"
	"go.source.hueristiq.com/logger/formatter"
)

var (
	soak           bool
	appendToOutput bool
	unique         bool
	preview        bool
	quiet          bool
	monochrome     bool

	au = aurora.New(aurora.WithColors(true))
)

func init() {
	pflag.BoolVar(&soak, "soak", false, "")
	pflag.BoolVarP(&appendToOutput, "append", "a", false, "")
	pflag.BoolVarP(&unique, "unique", "u", false, "")
	pflag.BoolVarP(&preview, "preview", "p", false, "")
	pflag.BoolVarP(&quiet, "quiet", "q", false, "")
	pflag.BoolVar(&monochrome, "monochrome", false, "")

	pflag.Usage = func() {
		logger.Info().Label("").Msg(configuration.BANNER(au))

		h := "USAGE:\n"
		h += fmt.Sprintf(" %s [OPTION]... <FILE>\n", configuration.NAME)

		h += "\nINPUT:\n"
		h += "     --soak bool          soak up all input before writing to file\n"

		h += "\nOUTPUT:\n"
		h += " -a, --append bool        append lines to output\n"
		h += " -u, --unique bool        output unique lines\n"
		h += " -p, --preview bool       preview new lines, without writing to file\n"
		h += " -q, --quiet bool         suppress output to stdout\n"
		h += "     --monochrome bool    display no color output\n"

		logger.Info().Label("").Msg(h)
		logger.Print().Msg("")
	}

	pflag.Parse()

	logger.DefaultLogger.SetFormatter(formatter.NewConsoleFormatter(&formatter.ConsoleFormatterConfiguration{
		Colorize: !monochrome,
	}))

	au = aurora.New(aurora.WithColors(!monochrome))
}

func main() {
	if !input.HasStdin() {
		logger.Fatal().Msgf(configuration.NAME + " expects input from standard input stream.")
	}

	destination := pflag.Arg(0)

	var err error

	var writer io.WriteCloser

	uniqueDestinationLinesMap := map[string]bool{}

	if destination != "" && unique && appendToOutput {
		uniqueDestinationLinesMap, err = readFileIntoMap(destination)
		if err != nil && !os.IsNotExist(err) {
			logger.Fatal().Msg(err.Error())
		}
	}

	if destination != "" && !preview {
		writer, err = getWriteCloser(destination, appendToOutput)
		if err != nil {
			logger.Fatal().Msg(err.Error())
		}

		defer writer.Close()
	}

	if soak {
		if err = processInputInSoakMode(uniqueDestinationLinesMap, destination, writer); err != nil {
			logger.Fatal().Msg(err.Error())
		}
	} else {
		if err = processInputInDefaultMode(uniqueDestinationLinesMap, destination, writer); err != nil {
			logger.Fatal().Msg(err.Error())
		}
	}
}

func processInputInSoakMode(uniqueDestinationLinesMap map[string]bool, destination string, df io.WriteCloser) (err error) {
	var inputLinesSlice []string

	inputLinesSlice, err = readStdinIntoSlice()
	if err != nil {
		return
	}

	for _, line := range inputLinesSlice {
		if unique {
			if uniqueDestinationLinesMap[line] {
				continue
			}

			uniqueDestinationLinesMap[line] = true
		}

		if !quiet {
			logger.Print().Msg(line)
		}

		if !preview && destination != "" {
			fmt.Fprintf(df, "%s\n", line)
		}
	}

	return
}

func processInputInDefaultMode(uniqueDestinationLinesMap map[string]bool, destination string, df io.WriteCloser) (err error) {
	scanner := bufio.NewScanner(os.Stdin)

	for scanner.Scan() {
		line := scanner.Text()

		if unique {
			if uniqueDestinationLinesMap[line] {
				continue
			}

			uniqueDestinationLinesMap[line] = true
		}

		if !quiet {
			logger.Print().Msg(line)
		}

		if !preview && destination != "" {
			fmt.Fprintf(df, "%s\n", line)
		}
	}

	if err = scanner.Err(); err != nil {
		return
	}

	return
}

func readFileIntoMap(file string) (lines map[string]bool, err error) {
	lines = map[string]bool{}

	var f *os.File

	f, err = os.Open(file)
	if err != nil {
		return
	}

	defer f.Close()

	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := scanner.Text()

		if _, ok := lines[line]; ok {
			continue
		}

		lines[line] = true
	}

	if err = scanner.Err(); err != nil {
		return
	}

	return
}

func getWriteCloser(file string, appendToFile bool) (writer io.WriteCloser, err error) {
	directory := filepath.Dir(file)

	if _, err = os.Stat(directory); os.IsNotExist(err) {
		if err = os.MkdirAll(directory, os.ModePerm); err != nil {
			return
		}
	}

	if appendToFile {
		writer, err = os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	} else {
		writer, err = os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	}

	return
}

func readStdinIntoSlice() (lines []string, err error) {
	lines = []string{}

	scanner := bufio.NewScanner(os.Stdin)

	for scanner.Scan() {
		line := scanner.Text()

		lines = append(lines, line)
	}

	if err = scanner.Err(); err != nil {
		return
	}

	return
}
