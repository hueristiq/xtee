// Package input provides helpers for inspecting the program's input streams.
package input

import "os"

// HasStdin reports whether standard input carries piped or redirected data.
// It returns true when stdin is not a terminal (character device), and false
// when stdin is a terminal or cannot be stat'ed.
func HasStdin() bool {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return false
	}

	return (stat.Mode() & os.ModeCharDevice) == 0
}
