package backup

import (
	"io"
	"os"
	"path/filepath"

	"github.com/portainer/portainer/api/edgestack"
)

// BusinessStore is optional so archives from installations without the module
// remain supported. The shared OfflineGate is held while these hooks execute.
type BusinessStore interface {
	BackupTo(io.Writer) error
	Close() error
}

func firstBusiness(stores []BusinessStore) BusinessStore {
	if len(stores) > 0 {
		return stores[0]
	}
	return nil
}

func backupBusiness(directory string, store BusinessStore) error {
	if store == nil {
		return nil
	}
	dir := filepath.Join(directory, "edgestack")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "business.db"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err := store.BackupTo(f); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func validateBusinessRestore(directory string) error {
	p := filepath.Join(directory, "edgestack", "business.db")
	info, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return nil
	} // Older Portainer archive.
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return os.ErrInvalid
	}
	return edgestack.ValidateBackup(p)
}

func restoreBusiness(source, destination string) error {
	dir := filepath.Join(destination, "edgestack")
	// Make the fail-closed recovery marker durable before changing any database
	// file. A failed restore or interrupted rename must never revive old tokens.
	if err := prepareBusinessRestore(destination); err != nil {
		return err
	}
	src, err := os.Open(filepath.Join(source, "edgestack", "business.db"))
	if os.IsNotExist(err) {
		if err := os.Remove(filepath.Join(dir, "business.db")); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else {
		if err != nil {
			return err
		}
		defer src.Close()
		f, err := os.CreateTemp(dir, ".restore-business-*")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		if _, err := io.Copy(f, src); err != nil {
			_ = f.Close()
			return err
		}
		if err := f.Sync(); err != nil {
			_ = f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		if err := os.Rename(f.Name(), filepath.Join(dir, "business.db")); err != nil {
			return err
		}
	}
	// Secrets are intentionally excluded. The durable marker is consumed only
	// after the restored database has committed credential revocation.
	return syncBusinessDirectory(dir)
}

func prepareBusinessRestore(destination string) error {
	return edgestack.PrepareRestore(filepath.Join(destination, "edgestack"))
}

func syncBusinessDirectory(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
