// Package backup creates, verifies, and restores Arivu data backups.
//
// A backup is a directory holding a SQLite-consistent snapshot named
// arivu.sqlite3, a copy of the adjacent arivu.sqlite3.assets directory, and a
// manifest.json with the size and SHA-256 of every file. The native installer
// (arivu-installer backup/restore) and the container commands (arivu
// backup/restore) share this format, so a backup from one restores with the
// other.
package backup

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// DatabaseName is the snapshot's file name inside a backup directory.
const DatabaseName = "arivu.sqlite3"

// AssetsName is the asset directory's name inside a backup directory.
const AssetsName = DatabaseName + ".assets"

// Create writes a backup of dbPath and its adjacent assets directory into a
// new UTC-timestamped directory under parentDir and returns that directory.
func Create(dbPath, parentDir string) (string, error) {
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("primary database %s is missing", dbPath)
		}
		return "", err
	}
	targetDir := filepath.Join(parentDir, time.Now().UTC().Format("20060102T150405Z"))
	if err := os.MkdirAll(targetDir, 0o750); err != nil {
		return "", err
	}
	target := filepath.Join(targetDir, DatabaseName)
	if err := SQLiteSnapshot(dbPath, target); err != nil {
		return "", err
	}
	manifest := Manifest{Version: 1}
	if err := addManifestFile(target, DatabaseName, &manifest); err != nil {
		return "", err
	}
	assetSource := dbPath + ".assets"
	if _, err := os.Stat(assetSource); err == nil {
		err = filepath.Walk(assetSource, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil || info.IsDir() {
				return walkErr
			}
			rel, err := filepath.Rel(assetSource, path)
			if err != nil {
				return err
			}
			dst := filepath.Join(targetDir, AssetsName, rel)
			if strings.HasPrefix(filepath.ToSlash(rel), ".staging/") {
				return nil
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
				return err
			}
			if err := CopyFile(path, dst); err != nil {
				return err
			}
			return addManifestFile(dst, filepath.ToSlash(filepath.Join(AssetsName, rel)), &manifest)
		})
		if err != nil {
			return "", err
		}
	}
	raw, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(filepath.Join(targetDir, "manifest.json"), raw, 0o640); err != nil {
		return "", err
	}
	return targetDir, nil
}

// Manifest lists every file in a backup with its size and SHA-256.
type Manifest struct {
	Version int            `json:"version"`
	Files   []ManifestFile `json:"files"`
}

// ManifestFile is one manifest entry; Path is relative to the backup directory.
type ManifestFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func addManifestFile(path, name string, m *Manifest) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	m.Files = append(m.Files, ManifestFile{name, size, hex.EncodeToString(h.Sum(nil))})
	return nil
}

// VerifyManifest checks every file listed in dir/manifest.json. Backups made
// before manifests existed have none and are accepted.
func VerifyManifest(dir string) error {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var m Manifest
	if json.Unmarshal(raw, &m) != nil || m.Version != 1 {
		return errors.New("unsupported backup manifest")
	}
	for _, f := range m.Files {
		if filepath.IsAbs(f.Path) || strings.Contains(f.Path, "..") {
			return errors.New("invalid backup manifest path")
		}
		file, err := os.Open(filepath.Join(dir, filepath.FromSlash(f.Path)))
		if err != nil {
			return fmt.Errorf("backup integrity: %s: %w", f.Path, err)
		}
		h := sha256.New()
		size, copyErr := io.Copy(h, file)
		file.Close()
		if copyErr != nil || size != f.Size || hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
			return fmt.Errorf("backup integrity check failed for %s", f.Path)
		}
	}
	return nil
}

// SQLiteSnapshot writes a consistent copy of a live SQLite database with
// VACUUM INTO and checks the copy's integrity before moving it into place.
func SQLiteSnapshot(source string, target string) error {
	tmp := target + ".tmp"
	_ = os.Remove(tmp)
	db, err := sql.Open("sqlite3", source+"?_busy_timeout=5000")
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec("VACUUM INTO " + sqliteQuote(tmp)); err != nil {
		return err
	}
	check, err := sql.Open("sqlite3", tmp+"?_query_only=true")
	if err != nil {
		return err
	}
	defer check.Close()
	var result string
	if err := check.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("backup integrity check failed: %s", result)
	}
	return os.Rename(tmp, target)
}

func sqliteQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// CopyFile copies source to target with mode 0640, replacing target.
func CopyFile(source string, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// CopyTree copies a directory tree, creating directories with mode 0700.
func CopyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, e := filepath.Rel(src, path)
		if e != nil {
			return e
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		return CopyFile(path, target)
	})
}

// VerifyArtifactRows checks that every stored object the database refers to
// exists under assetRoot with the recorded size and SHA-256.
func VerifyArtifactRows(dbPath, assetRoot string) error {
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	queries := []string{
		`SELECT storage_key,byte_size,sha256 FROM artifacts WHERE deleted_at IS NULL`,
		`SELECT storage_key,byte_size,sha256 FROM bookmark_media WHERE deleted_at IS NULL`,
		`SELECT storage_key,byte_size,sha256 FROM public_share_media`,
	}
	for _, query := range queries {
		rows, err := db.Query(query)
		if err != nil { // Older backups may predate any one asset table.
			if strings.Contains(err.Error(), "no such table") {
				continue
			}
			return err
		}
		for rows.Next() {
			var key, digest string
			var expected int64
			if err = rows.Scan(&key, &expected, &digest); err != nil {
				_ = rows.Close()
				return err
			}
			if strings.Contains(key, "..") || filepath.IsAbs(key) {
				_ = rows.Close()
				return errors.New("invalid artifact storage key")
			}
			f, e := os.Open(filepath.Join(assetRoot, "objects", filepath.FromSlash(key)))
			if e != nil {
				_ = rows.Close()
				return fmt.Errorf("live artifact %s missing: %w", key, e)
			}
			h := sha256.New()
			n, e := io.Copy(h, f)
			_ = f.Close()
			if e != nil || n != expected || hex.EncodeToString(h.Sum(nil)) != digest {
				_ = rows.Close()
				return fmt.Errorf("live artifact %s integrity check failed", key)
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		_ = rows.Close()
	}
	return nil
}
