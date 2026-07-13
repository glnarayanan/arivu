package assets

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var ErrTooLarge = errors.New("artifact exceeds size limit")

type Store struct {
	root string
	max  int64
}

type ReconcileReport struct {
	StagingDeleted int
	ObjectsDeleted int
	Missing        []string
}

func New(dbPath string, max int64) (*Store, error) {
	root := dbPath + ".assets"
	if max <= 0 {
		max = 10 << 20
	}
	if err := os.MkdirAll(filepath.Join(root, ".staging"), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, "objects"), 0o700); err != nil {
		return nil, err
	}
	return &Store{root: root, max: max}, nil
}

// Reconcile removes only old, unreferenced files. refs must be a fully
// materialized snapshot so callers never hold a database cursor during the walk.
func (s *Store) Reconcile(refs map[string]struct{}, grace time.Duration, now time.Time, limit int) (ReconcileReport, error) {
	var out ReconcileReport
	if grace <= 0 {
		grace = 24 * time.Hour
	}
	if limit <= 0 {
		limit = 1000
	}
	cutoff := now.Add(-grace)
	clean := func(root string, staging bool) error {
		return filepath.WalkDir(root, func(p string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			if out.StagingDeleted+out.ObjectsDeleted >= limit {
				return filepath.SkipAll
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if !info.ModTime().Before(cutoff) {
				return nil
			}
			if !staging {
				rel, err := filepath.Rel(root, p)
				if err != nil {
					return err
				}
				key := filepath.ToSlash(rel)
				if _, live := refs[key]; live {
					return nil
				}
			}
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if staging {
				out.StagingDeleted++
			} else {
				out.ObjectsDeleted++
			}
			return nil
		})
	}
	if err := clean(filepath.Join(s.root, ".staging"), true); err != nil {
		return out, err
	}
	for key := range refs {
		p, err := s.path(key)
		if err != nil {
			out.Missing = append(out.Missing, key)
			continue
		}
		if _, err = os.Stat(p); errors.Is(err, os.ErrNotExist) {
			out.Missing = append(out.Missing, key)
		} else if err != nil {
			return out, err
		}
	}
	if err := clean(filepath.Join(s.root, "objects"), false); err != nil {
		return out, err
	}
	return out, nil
}

func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func (s *Store) Put(r io.Reader) (key, digest string, size int64, err error) {
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return
	}
	tmp := filepath.Join(s.root, ".staging", hex.EncodeToString(nonce[:]))
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", "", 0, err
	}
	defer func() {
		f.Close()
		if err != nil {
			os.Remove(tmp)
		}
	}()
	h := sha256.New()
	size, err = io.Copy(io.MultiWriter(f, h), io.LimitReader(r, s.max+1))
	if err != nil {
		return
	}
	if size > s.max {
		err = ErrTooLarge
		return
	}
	if err = f.Sync(); err != nil {
		return
	}
	digest = hex.EncodeToString(h.Sum(nil))
	key = digest[:2] + "/" + digest
	dst, e := s.path(key)
	if e != nil {
		err = e
		return
	}
	if err = os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return
	}
	if err = syncDir(filepath.Dir(filepath.Dir(dst))); err != nil {
		return
	}
	if err = os.Rename(tmp, dst); err != nil && !errors.Is(err, os.ErrExist) {
		return
	}
	if err = syncDir(filepath.Dir(dst)); err != nil {
		return
	}
	return
}

func (s *Store) Open(key string) (*os.File, error) {
	p, e := s.path(key)
	if e != nil {
		return nil, e
	}
	return os.Open(p)
}
func (s *Store) Remove(key string) error {
	p, e := s.path(key)
	if e != nil {
		return e
	}
	return os.Remove(p)
}
func (s *Store) path(key string) (string, error) {
	if strings.Contains(key, "\\") || filepath.IsAbs(key) || strings.Contains(key, "..") {
		return "", errors.New("invalid storage key")
	}
	p := filepath.Join(s.root, "objects", filepath.FromSlash(key))
	base := filepath.Join(s.root, "objects") + string(os.PathSeparator)
	if !strings.HasPrefix(p, base) {
		return "", fmt.Errorf("invalid storage key")
	}
	return p, nil
}
