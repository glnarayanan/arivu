package app

import (
	"net/http"

	"github.com/glnarayanan/arivu/internal/auth"
)

func retiredWorkflow(w http.ResponseWriter, r *http.Request, user auth.User) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusGone)
	_, _ = w.Write([]byte(`{"detail":"This planning workflow has been retired. Existing content is preserved in Notes and full JSON backups. Use /api/notes for new writing."}`))
}
