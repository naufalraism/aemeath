package securityaudit2

import (
	"strings"
	"testing"

	"github.com/naufalraism/aemeath/storage"
)

// Control and bidirectional characters were kept in stored names: "\u202e"
// makes "evil\u202egnp.exe" display as "evilexe.png", "\n" forges log lines.
func TestUpload_FilenameControlCharactersAreReplaced(t *testing.T) {
	store := storage.NewLocal(t.TempDir())
	for name, want := range map[string]string{
		"evil\u202egnp.exe":               "_evil_gnp.exe",
		"a\nINFO forged log line.png":     "_a_INFO forged log line.png",
		"tab\there.png":                   "_tab_here.png",
		"café 日本.png":                     "_café 日本.png", // ordinary letters are kept
		"\u200bzero\u2066width\u2069.png": "__zero_width_.png",
	} {
		stored, err := store.Save(t.Context(), name, strings.NewReader("x"))
		t.Logf("Save(%q) -> %q, err %v", name, stored, err)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(stored, want) {
			t.Errorf("Save(%q) stored %q, want a name ending in %q", name, stored, want)
		}
	}
}
