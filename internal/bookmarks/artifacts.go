package bookmarks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/glnarayanan/arivu/internal/assets"
	"github.com/glnarayanan/arivu/internal/auth"
)

var ErrArtifactQuota = errors.New("artifact quota exceeded")

func (s *Service) ReconcileAssets(ctx context.Context, store *assets.Store, grace time.Duration, limit int) (assets.ReconcileReport, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT storage_key FROM artifacts WHERE deleted_at IS NULL`)
	if err != nil {
		return assets.ReconcileReport{}, err
	}
	refs := make(map[string]struct{})
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			rows.Close()
			return assets.ReconcileReport{}, err
		}
		refs[key] = struct{}{}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return assets.ReconcileReport{}, err
	}
	return store.Reconcile(refs, grace, time.Now(), limit)
}

func (s *Service) commitArtifact(ctx context.Context, id, userID, bookmarkID, attemptID, evidenceID, kind, mime string, size int64, digest, key string) error {
	s.artifactMu.Lock()
	defer s.artifactMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existing int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM artifacts WHERE capture_attempt_id=? AND artifact_type=? AND deleted_at IS NULL`, attemptID, kind).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		return tx.Commit()
	}
	var used int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(byte_size),0) FROM artifacts WHERE user_id=? AND deleted_at IS NULL`, userID).Scan(&used); err != nil {
		return err
	}
	if s.artifactQuota > 0 && (size > s.artifactQuota || used > s.artifactQuota-size) {
		return ErrArtifactQuota
	}
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO artifacts(id,user_id,bookmark_id,capture_attempt_id,evidence_id,artifact_type,mime_type,byte_size,sha256,storage_key,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, id, userID, bookmarkID, attemptID, evidenceID, kind, mime, size, digest, key, nowString())
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) captureAttempts(ctx context.Context, userID, bookmarkID string) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT id,retry_of_id,status,requested_url,final_url,engine,engine_version,error_code,queued_at,COALESCE(started_at,''),COALESCE(finished_at,'') FROM capture_attempts WHERE user_id=? AND bookmark_id=? ORDER BY queued_at DESC`, userID, bookmarkID)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, retry, status, requested, final, engine, version, code, queued, started, finished string
		_ = rows.Scan(&id, &retry, &status, &requested, &final, &engine, &version, &code, &queued, &started, &finished)
		out = append(out, map[string]any{"id": id, "retry_of_id": retry, "status": status, "requested_url": requested, "final_url": final, "engine": engine, "engine_version": version, "error_code": code, "queued_at": queued, "started_at": started, "finished_at": finished})
	}
	return out
}

func (s *Service) bookmarkArtifacts(ctx context.Context, userID, bookmarkID string) []map[string]any {
	rows, err := s.db.QueryContext(ctx, `SELECT id,capture_attempt_id,COALESCE(evidence_id,''),artifact_type,mime_type,byte_size,sha256,created_at FROM artifacts WHERE user_id=? AND bookmark_id=? AND deleted_at IS NULL ORDER BY created_at DESC`, userID, bookmarkID)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, attempt, evidence, kind, mime, digest, created string
		var size int64
		_ = rows.Scan(&id, &attempt, &evidence, &kind, &mime, &size, &digest, &created)
		out = append(out, map[string]any{"id": id, "capture_attempt_id": attempt, "evidence_id": evidence, "type": kind, "mime_type": mime, "byte_size": size, "sha256": digest, "created_at": created, "download_url": "/api/artifacts/" + id + "/content"})
	}
	return out
}

func (s *Service) captureStatus(ctx context.Context, userID, bookmarkID string) string {
	var status string
	if s.db.QueryRowContext(ctx, `SELECT status FROM capture_attempts WHERE user_id=? AND bookmark_id=? ORDER BY queued_at DESC LIMIT 1`, userID, bookmarkID).Scan(&status) != nil {
		return "saved"
	}
	if status == "complete" {
		return "preserved"
	}
	if status == "partial" {
		return "partially_preserved"
	}
	if status == "queued" || status == "running" {
		return "processing"
	}
	return status
}

func (s *Service) ArtifactMetadata(w http.ResponseWriter, r *http.Request, user auth.User) {
	var id, bookmarkID, kind, mime, digest, created string
	var size int64
	err := s.db.QueryRowContext(r.Context(), `SELECT id,bookmark_id,artifact_type,mime_type,byte_size,sha256,created_at FROM artifacts WHERE id=? AND user_id=? AND deleted_at IS NULL`, r.PathValue("id"), user.ID).Scan(&id, &bookmarkID, &kind, &mime, &size, &digest, &created)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, "Artifact not found")
		return
	}
	if err != nil {
		writeError(w, 500, "Could not load artifact")
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "bookmark_id": bookmarkID, "type": kind, "mime_type": mime, "byte_size": size, "sha256": digest, "created_at": created})
}

func (s *Service) ArtifactContent(w http.ResponseWriter, r *http.Request, user auth.User) {
	var key, mime, kind string
	var size int64
	err := s.db.QueryRowContext(r.Context(), `SELECT storage_key,mime_type,artifact_type,byte_size FROM artifacts WHERE id=? AND user_id=? AND deleted_at IS NULL`, r.PathValue("id"), user.ID).Scan(&key, &mime, &kind, &size)
	if errors.Is(err, sql.ErrNoRows) || s.assets == nil {
		writeError(w, 404, "Artifact not found")
		return
	}
	if err != nil {
		writeError(w, 500, "Could not load artifact")
		return
	}
	f, err := s.assets.Open(key)
	if err != nil {
		writeError(w, 404, "Artifact content missing")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	disposition := "attachment"
	if kind == "screenshot" || kind == "pdf" {
		disposition = "inline"
	}
	w.Header().Set("Content-Disposition", disposition+`; filename="`+kind+`"`)
	w.Header().Set("Content-Length", fmt.Sprint(size))
	_, _ = io.Copy(w, f)
}
