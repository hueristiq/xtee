// Package input provides helpers for inspecting the program's input streams.
package input

import "os"

// HasStdin reports whether standard input carries piped or redirected data.
// It returns true when stdin is a pipe or a redirected file, and false when
// stdin is a terminal (character device) or cannot be stat'ed.
func HasStdin() bool {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return false
	}

	isNotCharDevice := (stat.Mode() & os.ModeCharDevice) == 0
	isNamedPipe := (stat.Mode() & os.ModeNamedPipe) != 0

	return isNotCharDevice || isNamedPipe
}
