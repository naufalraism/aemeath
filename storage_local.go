package aemeath

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/naufalraism/aemeath/storage"
)

// LocalStorage stores files on the local disk. It behaves exactly like
// storage.Local, which does the work.
//
// Deprecated: use storage.Local and storage.NewLocal. LocalStorage will be
// removed before v1.0.
type LocalStorage struct {
	UploadDir string
	FileMode  os.FileMode // default 0600
	DirMode   os.FileMode // default 0750
}

// NewLocalStorage creates a LocalStorage, creating the directory if needed.
//
// Deprecated: use storage.NewLocal. NewLocalStorage will be removed before
// v1.0.
func NewLocalStorage(uploadDir string) *LocalStorage {
	l := storage.NewLocal(uploadDir)
	return &LocalStorage{UploadDir: l.UploadDir}
}

func (s *LocalStorage) local() *storage.Local {
	return &storage.Local{UploadDir: s.UploadDir, FileMode: s.FileMode, DirMode: s.DirMode}
}

// Save writes src to a new file and returns the stored name; see
// storage.Local.Save.
func (s *LocalStorage) Save(filename string, src io.Reader) (string, error) {
	return s.local().Save(filename, src)
}

// Delete removes a stored file. A missing file is not an error.
func (s *LocalStorage) Delete(filename string) error {
	return s.local().Delete(filename)
}

// IsAllowedFile reports whether the file's extension is in allowedExts
// (compared case-insensitively, e.g. []string{".png", ".jpg"}).
//
// Deprecated: the extension is chosen by the client, so it says nothing
// about the content. Use the accept tag on a *multipart.FileHeader field
// (accept:"image/png,image/jpeg"), which checks the file's bytes. This
// method will be removed in v1.0.
func (s *LocalStorage) IsAllowedFile(filename string, allowedExts []string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	for _, allowed := range allowedExts {
		if ext == strings.ToLower(allowed) {
			return true
		}
	}
	return false
}
