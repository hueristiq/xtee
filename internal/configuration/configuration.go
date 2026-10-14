package configuration

import "github.com/logrusorgru/aurora/v4"

const Name = "xtee"

var Version = "1.0.0"

func Banner(au *aurora.Aurora) (banner string) {
	banner = au.Sprintf(
		au.BrightBlue(`
      _
__  _| |_ ___  ___
\ \/ / __/ _ \/ _ \
 >  <| ||  __/  __/
/_/\_\\__\___|\___|
             %s`).Bold(),
		au.BrightRed("v"+Version).Bold().Italic(),
	) + "\n\n"

	return banner
}
