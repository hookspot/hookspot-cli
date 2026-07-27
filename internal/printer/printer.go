package printer

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"hookspot/internal/ws"
)

// Printer renders deliveries and their outcomes as terminal lines, one
// "--> METHOD /path [attempt-uid]" line per delivery and, when wrapping a
// forwarding handler, a "<-- [status]" line for the local server's reply.
type Printer struct {
	out       io.Writer
	printBody bool
	now       func() time.Time
}

// New returns a Printer writing to out. When printBody is true, request
// bodies are printed after the summary line.
func New(out io.Writer, printBody bool) *Printer {
	return &Printer{out: out, printBody: printBody, now: time.Now}
}

// Handle prints the delivery and acks it with 200 without forwarding.
func (p *Printer) Handle(d ws.Delivery) (ws.Response, error) {
	p.request(d)
	return ws.Response{Status: http.StatusOK}, nil
}

// Wrap returns a handler that prints the delivery, invokes next, and prints
// the status of its response.
func (p *Printer) Wrap(next ws.Handler) ws.Handler {
	return func(d ws.Delivery) (ws.Response, error) {
		p.request(d)
		resp, err := next(d)
		if err != nil {
			return resp, err
		}
		fmt.Fprintf(p.out, "%s <-- [%d] %s %s\n", p.timestamp(), resp.Status, method(d), d.Path)
		return resp, nil
	}
}

func (p *Printer) request(d ws.Delivery) {
	fmt.Fprintf(p.out, "%s --> %s %s [%s]\n", p.timestamp(), method(d), d.Path, d.AttemptUID)
	if p.printBody && len(d.Body) > 0 {
		fmt.Fprintf(p.out, "%s\n", d.Body)
	}
}

func (p *Printer) timestamp() string {
	return p.now().Format("15:04:05")
}

// method mirrors proxy.Forwarder, which sends POST when the delivery carries
// no method.
func method(d ws.Delivery) string {
	if d.Method == "" {
		return http.MethodPost
	}
	return d.Method
}
