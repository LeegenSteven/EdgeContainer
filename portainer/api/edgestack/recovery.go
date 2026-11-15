package edgestack

import (
	"os"
	"path/filepath"
)

// RestoreBootstrapGuard survives recovery and later database replacements.
// Restored instances accept new registrations through explicit administrator
// imports, never by replaying an older operator file on a subsequent restart.
const RestoreBootstrapGuard = "restore-bootstrap-disabled"

func StartupBootstrapDisabled(directory string) (bool, error) {
	for _, name := range []string{RestoreMarker, RestoreBootstrapGuard} {
		if _, err := os.Lstat(filepath.Join(directory, name)); err == nil {
			return true, nil
		} else if !os.IsNotExist(err) {
			return false, err
		}
	}
	return false, nil
}

// PrepareRestore durably disables future startup seeding before either database
// is closed or replaced. The pending marker separately controls policy replay.
func PrepareRestore(directory string) error {
	if err := preserveRestoreBootstrapGuard(directory); err != nil {
		return err
	}
	return writeRecoveryMarker(directory, RestoreMarker)
}

func preserveRestoreBootstrapGuard(directory string) error {
	return writeRecoveryMarker(directory, RestoreBootstrapGuard)
}

func writeRecoveryMarker(directory, name string) error {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(directory, ".restore-state-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write([]byte("1\n")); err != nil {
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
	if err := os.Rename(f.Name(), filepath.Join(directory, name)); err != nil {
		return err
	}
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
