package jobs

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/glnarayanan/arivu/internal/database"
)

func TestLeaseClaimsReadyQueuedJob(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	queue := New(db)
	jobID, err := queue.EnqueueAt(ctx, "", "bookmark.process", `{"url":"https://example.com"}`, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	job, ok, err := queue.Lease(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || job.ID != jobID {
		t.Fatalf("Lease() = job:%#v ok:%v, want %s", job, ok, jobID)
	}
}

func TestLeaseRecoversExpiredLeasedJob(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	queue := New(db)
	jobID, err := queue.EnqueueAt(ctx, "", "bookmark.process", `{"url":"https://example.com"}`, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	oldLease := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	if _, err := db.ExecContext(ctx, `UPDATE jobs SET status='leased', leased_until=?, attempts=1 WHERE id=?`, oldLease, jobID); err != nil {
		t.Fatal(err)
	}

	job, ok, err := queue.Lease(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || job.ID != jobID {
		t.Fatalf("Lease() = job:%#v ok:%v, want recovered %s", job, ok, jobID)
	}
	var attempts int
	var leaseUntil string
	if err := db.QueryRowContext(ctx, `SELECT attempts,leased_until FROM jobs WHERE id=?`, jobID).Scan(&attempts, &leaseUntil); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || leaseUntil == oldLease {
		t.Fatalf("expired lease was not renewed: attempts=%d leased_until=%q", attempts, leaseUntil)
	}
}

func TestLeaseDoesNotStealActiveLease(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	queue := New(db)
	jobID, err := queue.EnqueueAt(ctx, "", "bookmark.process", `{"url":"https://example.com"}`, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE jobs SET status='leased', leased_until=?, attempts=1 WHERE id=?`, time.Now().Add(time.Minute).UTC().Format(time.RFC3339), jobID); err != nil {
		t.Fatal(err)
	}

	if job, ok, err := queue.Lease(ctx, time.Minute); err != nil || ok {
		t.Fatalf("Lease() = job:%#v ok:%v err:%v, want no lease", job, ok, err)
	}
}

func TestFailReportsTerminalState(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	queue := New(db)
	jobID, err := queue.EnqueueAt(ctx, "", "bookmark.process", `{}`, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := queue.Lease(ctx, time.Minute); err != nil {
		t.Fatal(err)
	}

	terminal, err := queue.Fail(ctx, jobID, "first failure")
	if err != nil {
		t.Fatal(err)
	}
	if terminal {
		t.Fatal("first failure should remain retryable")
	}
	if _, err := db.ExecContext(ctx, `UPDATE jobs SET attempts=max_attempts WHERE id=?`, jobID); err != nil {
		t.Fatal(err)
	}
	terminal, err = queue.Fail(ctx, jobID, "final failure")
	if err != nil {
		t.Fatal(err)
	}
	if !terminal {
		t.Fatal("max-attempt failure should be terminal")
	}
}
