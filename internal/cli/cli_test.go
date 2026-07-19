package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishReadyAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ready")
	if err := publishReady(path, "127.0.0.1:43210"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "127.0.0.1:43210\n" {
		t.Fatalf("ready file = %q", got)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("ready file mode = %o", st.Mode().Perm())
	}
}
