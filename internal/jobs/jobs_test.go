package jobs

import (
	"context"
	"database/sql"
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
	job, ok, err := queue.Lease(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected retryable job lease")
	}
	terminal, active, err := queue.Fail(ctx, job, "first failure")
	if err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Fatal("first failure should update active lease")
	}
	if terminal {
		t.Fatal("first failure should remain retryable")
	}
	if _, err := db.ExecContext(ctx, `UPDATE jobs SET attempts=max_attempts,run_after=? WHERE id=?`, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), jobID); err != nil {
		t.Fatal(err)
	}
	job, ok, err = queue.Lease(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected terminal job lease")
	}
	terminal, active, err = queue.Fail(ctx, job, "final failure")
	if err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Fatal("terminal failure should update active lease")
	}
	if !terminal {
		t.Fatal("max-attempt failure should be terminal")
	}
}

func TestCompleteSkipsStaleLease(t *testing.T) {
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
	staleJob, ok, err := queue.Lease(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected lease")
	}
	newLease := time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339)
	if _, err := db.ExecContext(ctx, `UPDATE jobs SET leased_until=?, attempts=attempts+1 WHERE id=?`, newLease, jobID); err != nil {
		t.Fatal(err)
	}

	completed, err := queue.Complete(ctx, staleJob)
	if err != nil {
		t.Fatal(err)
	}
	if completed {
		t.Fatal("stale lease should not complete job")
	}
	var status, leasedUntil string
	if err := db.QueryRowContext(ctx, `SELECT status,leased_until FROM jobs WHERE id=?`, jobID).Scan(&status, &leasedUntil); err != nil {
		t.Fatal(err)
	}
	if status != "leased" || leasedUntil != newLease {
		t.Fatalf("stale complete mutated job: status=%q leased_until=%q", status, leasedUntil)
	}
}

func TestFailSkipsStaleLease(t *testing.T) {
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
	staleJob, ok, err := queue.Lease(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected lease")
	}
	newLease := time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339)
	if _, err := db.ExecContext(ctx, `UPDATE jobs SET leased_until=?, attempts=max_attempts WHERE id=?`, newLease, jobID); err != nil {
		t.Fatal(err)
	}

	terminal, active, err := queue.Fail(ctx, staleJob, "late failure")
	if err != nil {
		t.Fatal(err)
	}
	if terminal || active {
		t.Fatalf("stale lease Fail() terminal=%v active=%v, want false false", terminal, active)
	}
	var status, leasedUntil string
	var lastError sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT status,leased_until,last_error FROM jobs WHERE id=?`, jobID).Scan(&status, &leasedUntil, &lastError); err != nil {
		t.Fatal(err)
	}
	if status != "leased" || leasedUntil != newLease || lastError.Valid {
		t.Fatalf("stale fail mutated job: status=%q leased_until=%q last_error=%q", status, leasedUntil, lastError.String)
	}
}
