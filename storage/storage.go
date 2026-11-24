// Package storage saves uploaded files.
//
// Storage is the contract a backend implements; Local keeps files on disk.
// Register one for handlers with Provide, under the interface type:
//
//	aemeath.Provide[storage.Storage](app, storage.NewLocal("uploads"))
//
// and read it in a handler with aemeath.MustGet[storage.Storage](c).
//
// The package uses only the standard library and does not import aemeath.
package storage

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// Storage is the contract implemented by every file storage backend.
type Storage interface {
	// Save stores src under a new name derived from filename and returns
	// that name.
	Save(filename string, src io.Reader) (string, error)
	// Delete removes a stored file. A missing file is not an error.
	Delete(filename string) error
}

// Local stores files on the local disk.
//
// Files are private to the user running the server by default (mode 0600, in a
// 0750 directory), because uploads often hold personal documents. If another
// program must read them (a reverse proxy serving /uploads, for example), set
// FileMode and DirMode, for example 0o644 and 0o755.
type Local struct {
	UploadDir string
	FileMode  os.FileMode // default 0600
	DirMode   os.FileMode // default 0750
}

func (s *Local) fileMode() os.FileMode {
	if s.FileMode == 0 {
		return 0o600
	}
	return s.FileMode
}

func (s *Local) dirMode() os.FileMode {
	if s.DirMode == 0 {
		return 0o750
	}
	return s.DirMode
}

// NewLocal creates a Local storage, creating the directory if needed.
func NewLocal(uploadDir string) *Local {
	s := &Local{UploadDir: uploadDir}
	_ = os.MkdirAll(uploadDir, s.dirMode()) // Save retries and reports any error
	return s
}

// safeBase strips any directory part (both / and \ separators) from a
// client-supplied file name so it can never escape UploadDir. Trailing dots
// and spaces are removed too, because Windows drops them when it creates the
// file ("x.html." would become "x.html").
// cleanName replaces control characters (newlines, tabs, NUL) and invisible
// formatting characters (right-to-left override, zero-width space, ...) with
// "_". Kept, "evil\u202Egnp.exe" displays as "evilexe.png" in a file list,
// and a newline forges lines in a log that prints the name.
// cleanName replaces control and invisible format characters, and the
// characters Windows does not allow in a file name (<>:"|?*), with "_" on
// every OS, so a stored name means the same file everywhere. A colon matters
// most: on NTFS "x.html::$DATA" writes the file x.html (and "a.png:b" a
// hidden stream of a.png), slipping past the check that renames .html.
func cleanName(name string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || strings.ContainsRune(`<>:"|?*`, r) {
			return '_'
		}
		return r
	}, name)
}

func safeBase(filename string) string {
	if i := strings.LastIndexAny(filename, `/\`); i >= 0 {
		filename = filename[i+1:]
	}
	filename = strings.TrimRight(filepath.Base(cleanName(filename)), ". ")
	if filename == "" || filename == string(filepath.Separator) {
		return "file"
	}
	return filename
}

// activeExts are extensions a browser renders or runs (HTML, SVG, XML,
// JavaScript), or that a web server executes. An upload with one of them,
// served from your origin, is stored XSS or worse, whatever its first bytes
// look like (a PNG header followed by <script> passes accept:"image/png").
var activeExts = map[string]bool{
	".html": true, ".htm": true, ".xhtml": true, ".xht": true, ".shtml": true, ".mht": true, ".mhtml": true,
	".svg": true, ".svgz": true, ".xml": true, ".xsl": true, ".xslt": true,
	".js": true, ".mjs": true, ".cjs": true, ".swf": true, ".hta": true, ".htc": true,
	".php": true, ".phtml": true, ".asp": true, ".aspx": true, ".jsp": true, ".cgi": true,
}

// neutralize appends ".txt" to a name with an active extension, so the file
// is served as plain text by Static, http.FileServer, nginx and the like.
func neutralize(name string) string {
	if activeExts[strings.ToLower(filepath.Ext(name))] {
		return name + ".txt"
	}
	return name
}

// Save writes src to a new file and returns the stored name. The stored name
// is "<unix time>_<random>_<original name>", so uploads with the same original
// name never overwrite each other. Names with an extension a browser would
// render or run (.html, .svg, .js, ...) get ".txt" appended ("x.html" is
// stored as "..._x.html.txt"), so serving the upload folder cannot become
// stored XSS. Check the type with the accept tag to refuse
// such files instead.
func (s *Local) Save(filename string, src io.Reader) (string, error) {
	if err := os.MkdirAll(s.UploadDir, s.dirMode()); err != nil {
		return "", fmt.Errorf("storage: failed to create upload directory: %w", err)
	}

	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", fmt.Errorf("storage: failed to generate file name: %w", err)
	}
	stored := fmt.Sprintf("%d_%s_%s", time.Now().Unix(), hex.EncodeToString(rnd[:]), neutralize(safeBase(filename)))
	destPath := filepath.Join(s.UploadDir, stored)

	destFile, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, s.fileMode())
	if err != nil {
		return "", fmt.Errorf("storage: failed to create file: %w", err)
	}

	if _, err := io.Copy(destFile, src); err != nil {
		_ = destFile.Close()
		_ = os.Remove(destPath)
		return "", fmt.Errorf("storage: failed to save file: %w", err)
	}
	if err := destFile.Close(); err != nil {
		_ = os.Remove(destPath)
		return "", fmt.Errorf("storage: failed to save file: %w", err)
	}
	return stored, nil
}

// Delete removes a stored file. A missing file is not an error.
func (s *Local) Delete(filename string) error {
	err := os.Remove(filepath.Join(s.UploadDir, safeBase(filename)))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("storage: failed to delete file: %w", err)
	}
	return nil
}

var _ Storage = (*Local)(nil)
