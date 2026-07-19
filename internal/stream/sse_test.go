package stream

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestFragmentationMultilineAndUTF8(t *testing.T) {
	raw := ": hi\r\nid: 7\r\nevent: delta\r\ndata: hé\r\ndata: 世界\r\n\r\n"
	for split := 1; split < len(raw); split++ {
		r := io.MultiReader(strings.NewReader(raw[:split]), strings.NewReader(raw[split:]))
		e, err := NewReader(r, 1024).Next()
		if err != nil || e.Event != "delta" || e.ID != "7" || e.Data != "hé\n世界" {
			t.Fatalf("split %d: %#v %v", split, e, err)
		}
	}
}

func TestBounded(t *testing.T) {
	_, err := NewReader(strings.NewReader("data: "+strings.Repeat("x", 20)+"\n\n"), 10).Next()
	if !errors.Is(err, ErrEventTooLarge) {
		t.Fatalf("%v", err)
	}
}

func TestWriteRoundTrip(t *testing.T) {
	var b bytes.Buffer
	e := Event{Event: "x", ID: "i", Data: "a\nb"}
	if err := Write(&b, e); err != nil {
		t.Fatal(err)
	}
	got, err := NewReader(&b, 100).Next()
	if err != nil || got.Data != e.Data {
		t.Fatalf("%#v %v", got, err)
	}
}
