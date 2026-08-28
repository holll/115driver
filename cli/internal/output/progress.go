package output

import (
	"fmt"
	"io"
	"os"

	"github.com/cheggaaa/pb/v3"
)

func CreateProgressBar(total int64) *pb.ProgressBar {
	if !IsTerminal() {
		return nil
	}
	bar := pb.Start64(total)
	bar.SetTemplateString(`{{counters . }} {{bar . }} {{percent . }} {{speed . }}`)
	return bar
}

// IsTerminal reports whether stdout is attached to a terminal (a TTY), which is
// when interactive progress output is appropriate.
func IsTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func TrackProgress(r io.Reader, total int64) io.Reader {
	bar := CreateProgressBar(total)
	if bar == nil {
		return r
	}
	return bar.NewProxyReader(r)
}

func FinishProgress(bar *pb.ProgressBar) {
	if bar != nil {
		bar.Finish()
	}
}

func PrintProgress(done, total int64) {
	fmt.Printf("\rProgress: %s / %s", FormatFileSize(done), FormatFileSize(total))
}
