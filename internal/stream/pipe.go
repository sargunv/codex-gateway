package stream

import (
	"context"
	"io"
)

type flushWriter interface{ Flush() }

func Pipe(ctx context.Context, dst io.Writer, src io.Reader, maxEvent int) error {
	r := NewReader(src, maxEvent)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		e, err := r.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err = Write(dst, e); err != nil {
			return err
		}
		if f, ok := dst.(flushWriter); ok {
			f.Flush()
		}
	}
}

func Copy(ctx context.Context, dst io.Writer, src io.Reader) error {
	buf := make([]byte, 32<<10)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return werr
			}
			if f, ok := dst.(flushWriter); ok {
				f.Flush()
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
