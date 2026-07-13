package assets

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreRoundTripTraversalAndLimit(t *testing.T) {
	s, err := New(t.TempDir()+"/db.sqlite3", 4)
	if err != nil {
		t.Fatal(err)
	}
	key, digest, size, err := s.Put(strings.NewReader("test"))
	if err != nil || digest == "" || size != 4 {
		t.Fatalf("Put = %q %q %d %v", key, digest, size, err)
	}
	f, err := s.Open(key)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	if string(b) != "test" {
		t.Fatalf("content = %q", b)
	}
	if _, err := s.Open("../../etc/passwd"); err == nil {
		t.Fatal("traversal accepted")
	}
	if _, _, _, err = s.Put(strings.NewReader("large")); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("limit error = %v", err)
	}
}

func TestReconcileLifecycleAndSafety(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "db.sqlite3"), 100)
	if err != nil {
		t.Fatal(err)
	}
	referenced, _, _, err := s.Put(strings.NewReader("live"))
	if err != nil {
		t.Fatal(err)
	}
	orphan, _, _, err := s.Put(strings.NewReader("orphan"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	old := now.Add(-2 * time.Hour)
	for _, key := range []string{referenced, orphan} {
		p, _ := s.path(key)
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	fresh, _, _, err := s.Put(strings.NewReader("fresh"))
	if err != nil {
		t.Fatal(err)
	}
	staleStage := filepath.Join(s.root, ".staging", "stale")
	if err := os.WriteFile(staleStage, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(staleStage, old, old); err != nil {
		t.Fatal(err)
	}
	refs := map[string]struct{}{referenced: {}, "aa/missing": {}, "../../unsafe": {}}
	report, err := s.Reconcile(refs, time.Hour, now, 100)
	if err != nil {
		t.Fatal(err)
	}
	if report.ObjectsDeleted != 1 || report.StagingDeleted != 1 || len(report.Missing) != 2 {
		t.Fatalf("report = %+v", report)
	}
	if _, err := s.Open(referenced); err != nil {
		t.Fatalf("referenced/shared key removed: %v", err)
	}
	if _, err := s.Open(fresh); err != nil {
		t.Fatalf("fresh orphan removed: %v", err)
	}
	if _, err := s.Open(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old orphan remains: %v", err)
	}
	if _, err := os.Stat(staleStage); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale staging remains: %v", err)
	}
}
