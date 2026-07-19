// Package stream implements bounded Server-Sent Events parsing and piping.
package stream

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

var (
	ErrEventTooLarge = errors.New("SSE event exceeds configured limit")
	ErrIdleTimeout   = errors.New("SSE idle timeout")
)

type Event struct {
	Event, Data, ID string
	Retry           string
	Comments        []string
}
type Reader struct {
	r        *bufio.Reader
	MaxBytes int
}

func NewReader(r io.Reader, max int) *Reader {
	if max <= 0 {
		max = 1 << 20
	}
	return &Reader{r: bufio.NewReaderSize(r, 4096), MaxBytes: max}
}

func (r *Reader) Next() (Event, error) {
	var e Event
	var data []string
	size := 0
	for {
		line, err := r.r.ReadString('\n')
		size += len(line)
		if size > r.MaxBytes {
			return e, ErrEventTooLarge
		}
		if err != nil && len(line) == 0 {
			return e, err
		}
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			if len(data) == 0 && e.Event == "" && e.ID == "" && len(e.Comments) == 0 {
				if err != nil {
					return e, err
				}
				continue
			}
			e.Data = strings.Join(data, "\n")
			return e, nil
		}
		if strings.HasPrefix(line, ":") {
			e.Comments = append(e.Comments, strings.TrimPrefix(line, ":"))
		} else {
			field, value, ok := strings.Cut(line, ":")
			if ok {
				value = strings.TrimPrefix(value, " ")
			} else {
				value = ""
			}
			switch field {
			case "event":
				e.Event = value
			case "data":
				data = append(data, value)
			case "id":
				if !strings.ContainsRune(value, 0) {
					e.ID = value
				}
			case "retry":
				e.Retry = value
			}
		}
		if err != nil {
			e.Data = strings.Join(data, "\n")
			return e, nil
		}
	}
}

func (r *Reader) NextContext(ctx context.Context, idle time.Duration, closer io.Closer) (Event, error) {
	if idle <= 0 {
		return r.Next()
	}
	type result struct {
		event Event
		err   error
	}
	ch := make(chan result, 1)
	go func() { e, err := r.Next(); ch <- result{e, err} }()
	t := time.NewTimer(idle)
	defer t.Stop()
	select {
	case x := <-ch:
		return x.event, x.err
	case <-ctx.Done():
		if closer != nil {
			_ = closer.Close()
		}
		return Event{}, ctx.Err()
	case <-t.C:
		if closer != nil {
			_ = closer.Close()
		}
		return Event{}, ErrIdleTimeout
	}
}

func Write(w io.Writer, e Event) error {
	for _, c := range e.Comments {
		if _, err := fmt.Fprintf(w, ":%s\n", c); err != nil {
			return err
		}
	}
	if e.Event != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", e.Event); err != nil {
			return err
		}
	}
	if e.ID != "" {
		if _, err := fmt.Fprintf(w, "id: %s\n", e.ID); err != nil {
			return err
		}
	}
	for _, line := range strings.Split(e.Data, "\n") {
		if _, err := fmt.Fprintf(w, "data: %s\n", line); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "\n")
	return err
}
