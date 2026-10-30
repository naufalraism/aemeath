package aemeath

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LocalStorage stores files on the local disk.
//
// Files are private to the user running the server by default (mode 0600, in a
// 0750 directory), because uploads often hold personal documents. If another
// program must read them (a reverse proxy serving /uploads, for example), set
// FileMode and DirMode, for example 0o644 and 0o755.
type LocalStorage struct {
	UploadDir string
	FileMode  os.FileMode // default 0600
	DirMode   os.FileMode // default 0750
}

func (s *LocalStorage) fileMode() os.FileMode {
	if s.FileMode == 0 {
		return 0o600
	}
	return s.FileMode
}

func (s *LocalStorage) dirMode() os.FileMode {
	if s.DirMode == 0 {
		return 0o750
	}
	return s.DirMode
}

// NewLocalStorage creates a LocalStorage, creating the directory if needed.
func NewLocalStorage(uploadDir string) *LocalStorage {
	s := &LocalStorage{UploadDir: uploadDir}
	_ = os.MkdirAll(uploadDir, s.dirMode()) // Save retries and reports any error
	return s
}

// safeBase strips any directory part (both / and \ separators) from a
// client-supplied file name so it can never escape UploadDir.
func safeBase(filename string) string {
	if i := strings.LastIndexAny(filename, `/\`); i >= 0 {
		filename = filename[i+1:]
	}
	filename = filepath.Base(filename)
	if filename == "." || filename == ".." || filename == string(filepath.Separator) || filename == "" {
		return "file"
	}
	return filename
}

// Save writes src to a new file and returns the stored name. The stored name
// is "<unix time>_<random>_<original name>", so uploads with the same original
// name never overwrite each other.
func (s *LocalStorage) Save(filename string, src io.Reader) (string, error) {
	if err := os.MkdirAll(s.UploadDir, s.dirMode()); err != nil {
		return "", fmt.Errorf("aemeath: failed to create upload directory: %w", err)
	}

	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", fmt.Errorf("aemeath: failed to generate file name: %w", err)
	}
	stored := fmt.Sprintf("%d_%s_%s", time.Now().Unix(), hex.EncodeToString(rnd[:]), safeBase(filename))
	destPath := filepath.Join(s.UploadDir, stored)

	destFile, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, s.fileMode())
	if err != nil {
		return "", fmt.Errorf("aemeath: failed to create file: %w", err)
	}

	if _, err := io.Copy(destFile, src); err != nil {
		_ = destFile.Close()
		_ = os.Remove(destPath)
		return "", fmt.Errorf("aemeath: failed to save file: %w", err)
	}
	if err := destFile.Close(); err != nil {
		_ = os.Remove(destPath)
		return "", fmt.Errorf("aemeath: failed to save file: %w", err)
	}
	return stored, nil
}

// Delete removes a stored file. A missing file is not an error.
func (s *LocalStorage) Delete(filename string) error {
	err := os.Remove(filepath.Join(s.UploadDir, safeBase(filename)))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("aemeath: failed to delete file: %w", err)
	}
	return nil
}

// IsAllowedFile reports whether the file's extension is in allowedExts
// (compared case-insensitively, e.g. []string{".png", ".jpg"}).
func (s *LocalStorage) IsAllowedFile(filename string, allowedExts []string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	for _, allowed := range allowedExts {
		if ext == strings.ToLower(allowed) {
			return true
		}
	}
	return false
}
