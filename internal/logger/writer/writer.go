package writer

import "github.com/hueristiq/xtee/internal/logger/levels"

type Writer interface {
	Write(data []byte, level levels.Level)
}
