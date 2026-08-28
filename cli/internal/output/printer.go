package output

import (
	"encoding/json"
	"fmt"
	"os"
)

const (
	ExitSuccess  = 0
	ExitError    = 1
	ExitAuth     = 2
	ExitNotFound = 3
	ExitNetwork  = 4
	ExitArgs     = 5
)

type Envelope struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
	Code    int         `json:"code"`
}

type Printer struct {
	JSON bool
}

func NewPrinter(json bool) *Printer {
	return &Printer{JSON: json}
}

func (p *Printer) PrintSuccess(data interface{}) {
	if p.JSON {
		p.printJSON(Envelope{Success: true, Data: data, Code: 0})
	}
}

func (p *Printer) PrintError(msg string, code int) int {
	if p.JSON {
		p.printJSON(Envelope{Success: false, Error: msg, Code: code})
		return code
	}
	fmt.Fprintf(os.Stderr, "Error: %s\n", msg)
	return code
}

// PrintResult writes an envelope carrying data and an optional error message. It
// is used for outcomes that keep partial data but are not a clean success, e.g.
// a batch operation that partially failed: machines see success=false with the
// error field set, and still receive the partial data.
func (p *Printer) PrintResult(data interface{}, errMsg string, code int) int {
	if p.JSON {
		p.printJSON(Envelope{Success: errMsg == "", Data: data, Error: errMsg, Code: code})
		return code
	}
	if errMsg != "" {
		fmt.Fprintf(os.Stderr, "Error: %s\n", errMsg)
	}
	return code
}

func (p *Printer) printJSON(env Envelope) {
	bytes, _ := json.MarshalIndent(env, "", "  ")
	fmt.Println(string(bytes))
}
