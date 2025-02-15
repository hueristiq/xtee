package configuration

import "github.com/logrusorgru/aurora/v4"

const (
	NAME    = "xtee"
	VERSION = "0.4.0"
)

var BANNER = func(au *aurora.Aurora) (banner string) {
	banner = au.Sprintf(
		au.BrightBlue(`
      _
__  _| |_ ___  ___
\ \/ / __/ _ \/ _ \
 >  <| ||  __/  __/
/_/\_\\__\___|\___|
             %s`).Bold(),
		au.BrightRed("v"+VERSION).Bold().Italic(),
	) + "\n\n"

	return
}
