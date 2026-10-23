package backup

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RestoreHooks lets the caller stop and start whatever serves the database.
// Every hook is optional.
type RestoreHooks struct {
	// Stop runs after the backup is verified and staged, just before the live
	// files are swapped. Its error is ignored, as a stopped service is fine.
	Stop func()
	// Activate runs after the swap (ownership, start, health check). An error
	// rolls the swap back.
	Activate func() error
	// BeforeRollback and AfterRollback run around putting the previous files
	// back; their errors are reported with the cause.
	BeforeRollback func() error
	AfterRollback  func() []error
}

// Restore replaces dbPath and its adjacent assets directory with the contents
// of backupDir. It verifies the manifest and every stored object before
// touching live files, and puts the previous files back if anything fails.
func Restore(dbPath, backupDir string, hooks RestoreHooks) error {
	if backupDir == "" {
		return errors.New("backup directory is required")
	}
	if err := VerifyManifest(backupDir); err != nil {
		return err
	}
	source := filepath.Join(backupDir, DatabaseName)
	if _, err := os.Stat(source); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("primary backup database %s is missing", source)
		}
		return err
	}
	target := dbPath
	tmp := target + ".restore-tmp"
	if err := CopyFile(source, tmp); err != nil {
		return err
	}
	defer os.Remove(tmp)
	assetBackup := filepath.Join(backupDir, AssetsName)
	assetTmp := target + ".assets.restore-tmp"
	hasAssets := false
	if info, err := os.Stat(assetBackup); err == nil && info.IsDir() {
		hasAssets = true
		_ = os.RemoveAll(assetTmp)
		if err := CopyTree(assetBackup, assetTmp); err != nil {
			return err
		}
		defer os.RemoveAll(assetTmp)
	}
	if err := VerifyArtifactRows(tmp, assetTmp); err != nil {
		return err
	}
	if hooks.Stop != nil {
		hooks.Stop()
	}
	oldDB, oldAssets := target+".restore-previous", target+".assets.restore-previous"
	_ = os.Remove(oldDB)
	_ = os.RemoveAll(oldAssets)
	if _, err := os.Stat(target); err == nil {
		if err = os.Rename(target, oldDB); err != nil {
			return err
		}
	}
	rollback := func(cause error) error {
		var rollbackErrors []string
		if hooks.BeforeRollback != nil {
			if e := hooks.BeforeRollback(); e != nil {
				rollbackErrors = append(rollbackErrors, e.Error())
			}
		}
		_ = os.Remove(target)
		if e := os.Rename(oldDB, target); e != nil {
			rollbackErrors = append(rollbackErrors, e.Error())
		}
		if _, e := os.Stat(oldAssets); e == nil {
			_ = os.RemoveAll(target + ".assets")
			if e = os.Rename(oldAssets, target+".assets"); e != nil {
				rollbackErrors = append(rollbackErrors, e.Error())
			}
		}
		if hooks.AfterRollback != nil {
			for _, e := range hooks.AfterRollback() {
				rollbackErrors = append(rollbackErrors, e.Error())
			}
		}
		if len(rollbackErrors) > 0 {
			return fmt.Errorf("%w; rollback errors: %s", cause, strings.Join(rollbackErrors, "; "))
		}
		return cause
	}
	if hasAssets {
		if _, err := os.Stat(target + ".assets"); err == nil {
			if err = os.Rename(target+".assets", oldAssets); err != nil {
				return rollback(err)
			}
		}
	}
	if err := os.Rename(tmp, target); err != nil {
		return rollback(err)
	}
	if hasAssets {
		if err := os.Rename(assetTmp, target+".assets"); err != nil {
			return rollback(err)
		}
	}
	_ = os.Remove(target + "-wal")
	_ = os.Remove(target + "-shm")
	if hooks.Activate != nil {
		if err := hooks.Activate(); err != nil {
			return rollback(err)
		}
	}
	_ = os.Remove(oldDB)
	_ = os.RemoveAll(oldAssets)
	return nil
}

// ErrDatabaseInUse means another process has the database open.
var ErrDatabaseInUse = errors.New("the database is open in another process; stop the Arivu server first")

// EnsureNotInUse fails with ErrDatabaseInUse while another process holds
// dbPath open. A running Arivu server keeps its WAL-mode database open, so
// this stops a restore from replacing files underneath it. A missing database
// is not in use.
func EnsureNotInUse(dbPath string) error {
	if _, err := os.Stat(dbPath); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	db, err := sql.Open("sqlite3", "file:"+dbPath+"?_locking_mode=EXCLUSIVE&_busy_timeout=0")
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("BEGIN EXCLUSIVE; COMMIT"); err != nil {
		if strings.Contains(err.Error(), "locked") || strings.Contains(err.Error(), "busy") {
			return ErrDatabaseInUse
		}
		return err
	}
	return nil
}
