package authfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWritePreservesUnknownAndMode(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "auth.json")
	if err := os.WriteFile(p, []byte(`{"known":"x","unknown":{"nested":1}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	v["known"] = "y"
	if err = AtomicWrite(p, v); err != nil {
		t.Fatal(err)
	}
	got, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if Object(got, "unknown")["nested"].(float64) != 1 {
		t.Fatal("unknown field lost")
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", st.Mode().Perm())
	}
}
