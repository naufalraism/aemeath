package aemeath

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LocalStorage menyimpan file di disk lokal.
type LocalStorage struct {
	UploadDir string
}

func NewLocalStorage(uploadDir string) *LocalStorage {
	if _, err := os.Stat(uploadDir); os.IsNotExist(err) {
		os.MkdirAll(uploadDir, 0755)
	}
	return &LocalStorage{UploadDir: uploadDir}
}

func (s *LocalStorage) Save(filename string, src io.Reader) (string, error) {
	safeFilename := filepath.Base(filename)
	timestamp := time.Now().Unix()
	safeFilename = fmt.Sprintf("%d_%s", timestamp, safeFilename)
	destPath := filepath.Join(s.UploadDir, safeFilename)

	destFile, err := os.Create(destPath)
	if err != nil {
		return "", fmt.Errorf("aemeath: gagal membuat file: %w", err)
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, src); err != nil {
		return "", fmt.Errorf("aemeath: gagal menyimpan file: %w", err)
	}
	return safeFilename, nil
}

func (s *LocalStorage) Delete(filename string) error {
	safeFilename := filepath.Base(filename)
	err := os.Remove(filepath.Join(s.UploadDir, safeFilename))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("aemeath: gagal menghapus file: %w", err)
	}
	return nil
}

// IsAllowedFile mengecek ekstensi file yang diizinkan.
func (s *LocalStorage) IsAllowedFile(filename string, allowedExts []string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	for _, allowed := range allowedExts {
		if ext == allowed {
			return true
		}
	}
	return false
}
