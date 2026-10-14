package input

import (
	"os"

	"golang.org/x/term"
)

func HasStdin() (ok bool) {
	ok = !term.IsTerminal(int(os.Stdin.Fd()))

	return ok
}
