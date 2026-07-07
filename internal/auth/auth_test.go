package auth

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/glnarayanan/arivu/internal/config"
	"github.com/glnarayanan/arivu/internal/database"
)

func TestBootstrapAdminCreatesAndUpdatesAdmin(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "arivu.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	service := New(db, config.Config{SecretKey: "test-secret"})
	user, created, err := service.BootstrapAdmin(context.Background(), "Admin@Example.com", "first-password")
	if err != nil {
		t.Fatal(err)
	}
	if !created || user.Email != "admin@example.com" {
		t.Fatalf("unexpected created admin: user=%#v created=%v", user, created)
	}

	_, scheme, hash, err := service.userByEmail(context.Background(), "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if scheme != "argon2id" || !verifyPassword("first-password", scheme, hash) {
		t.Fatalf("bootstrap did not store an Argon2id password")
	}

	updated, created, err := service.BootstrapAdmin(context.Background(), "admin@example.com", "second-password")
	if err != nil {
		t.Fatal(err)
	}
	if created || updated.ID != user.ID {
		t.Fatalf("expected existing admin update, got user=%#v created=%v", updated, created)
	}
	_, scheme, hash, err = service.userByEmail(context.Background(), "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !verifyPassword("second-password", scheme, hash) || verifyPassword("first-password", scheme, hash) {
		t.Fatal("bootstrap update did not replace the admin password")
	}

	if _, _, err := service.BootstrapAdmin(context.Background(), "admin@example.com", "short"); err == nil {
		t.Fatal("expected short bootstrap password to fail")
	}
}
