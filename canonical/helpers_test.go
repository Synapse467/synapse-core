package canonical

import (
	"io"
	"strings"
)

func stringsReader(text string) io.Reader { return strings.NewReader(text) }
