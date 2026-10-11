// Package configuration provides xtee's identifying metadata: its name,
// version, and startup banner.
package configuration

import "github.com/logrusorgru/aurora/v4"

const (
	// Name is the program's command name as shown in usage output.
	Name = "xtee"

	// Version is the program's semantic version, rendered in the banner.
	Version = "1.0.0"
)

// Banner returns the ASCII-art startup banner with the version rendered in
// bright red, bold, and italic.
//
// Parameters:
//   - au (*aurora.Aurora): the colorizer; pass one with colors disabled for
//     monochrome output.
//
// Returns:
//   - banner (string): the formatted banner, ending with a blank line.
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
