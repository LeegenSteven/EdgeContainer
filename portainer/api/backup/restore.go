package backup

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/pkg/errors"
	"github.com/portainer/portainer/api/archive"
	"github.com/portainer/portainer/api/crypto"
	"github.com/portainer/portainer/api/database/boltdb"
	"github.com/portainer/portainer/api/dataservices"
	"github.com/portainer/portainer/api/filesystem"
	"github.com/portainer/portainer/api/http/offlinegate"

	"github.com/rs/zerolog/log"
)

var filesToRestore = append(filesToBackup, "portainer.db")

// Restores system state from backup archive, will trigger system shutdown, when finished.
func RestoreArchive(archive io.Reader, password string, filestorePath string, gate *offlinegate.OfflineGate, datastore dataservices.DataStore, shutdownTrigger context.CancelFunc, business ...BusinessStore) error {
	var err error
	if password != "" {
		archive, err = decrypt(archive, password)
		if err != nil {
			return errors.Wrap(err, "failed to decrypt the archive. Please ensure the password is correct and try again")
		}
	}

	restorePath := filesystem.JoinPaths(filestorePath, "restore", time.Now().Format("20060102150405"))
	defer func() {
		if err := os.RemoveAll(filepath.Dir(restorePath)); err != nil {
			log.Warn().Err(err).Msg("failed to clean up restore files")
		}
	}()

	if err := extractArchive(archive, restorePath); err != nil {
		return errors.Wrap(err, "cannot extract files from the archive. Please ensure the password is correct and try again")
	}
	restorePath, err = getRestoreSourcePath(restorePath)
	if err != nil {
		return errors.Wrap(err, "failed to find database in backup")
	}
	foundDatabase := false
	for _, name := range []string{boltdb.DatabaseFileName, boltdb.EncryptedDatabaseFileName} {
		if info, statErr := os.Stat(filepath.Join(restorePath, name)); statErr == nil && info.Mode().IsRegular() {
			foundDatabase = true
		}
	}
	if !foundDatabase {
		return errors.New("Portainer database missing from backup")
	}
	if err := validateBusinessRestore(restorePath); err != nil {
		return errors.Wrap(err, "invalid business database backup")
	}

	unlock := gate.Lock()
	defer unlock()
	// Establish crash recovery policy before closing or replacing either store.
	if err := prepareBusinessRestore(filestorePath); err != nil {
		return errors.Wrap(err, "failed to prepare business recovery marker")
	}
	// Once either store is closed, every exit path must stop the process. It
	// cannot safely resume serving requests against a closed or partial restore.
	if shutdownTrigger != nil {
		defer shutdownTrigger()
	}
	if store := firstBusiness(business); store != nil {
		if err := store.Close(); err != nil {
			return errors.Wrap(err, "Failed to stop business database")
		}
	}

	if err := datastore.Close(); err != nil {
		return errors.Wrap(err, "Failed to stop db")
	}

	if err := restoreFiles(restorePath, filestorePath); err != nil {
		return errors.Wrap(err, "failed to restore the system state")
	}
	if err := restoreBusiness(restorePath, filestorePath); err != nil {
		return errors.Wrap(err, "failed to restore business database")
	}

	return nil
}

func decrypt(r io.Reader, password string) (io.Reader, error) {
	return crypto.AesDecrypt(r, []byte(password))
}

func extractArchive(r io.Reader, destinationDirPath string) error {
	return archive.ExtractTarGz(r, destinationDirPath)
}

func getRestoreSourcePath(dir string) (string, error) {
	// find portainer.db or portainer.edb file. Return the parent directory
	var portainerdbRegex = regexp.MustCompile(`^portainer.e?db$`)

	backupDirPath := dir
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if portainerdbRegex.MatchString(d.Name()) {
			backupDirPath = filepath.Dir(path)
			return filepath.SkipDir
		}
		return nil
	})

	return backupDirPath, err
}

func restoreFiles(srcDir string, destinationDir string) error {
	for _, filename := range filesToRestore {
		if err := filesystem.CopyPath(filesystem.JoinPaths(srcDir, filename), destinationDir); err != nil {
			return err
		}
	}

	// TODO:  This is very boltdb module specific once again due to the filename.  Move to bolt module? Refactor for another day

	// Prevent the possibility of having both databases.  Remove any default new instance
	if err := os.Remove(filesystem.JoinPaths(destinationDir, boltdb.DatabaseFileName)); err != nil && !os.IsNotExist(err) {
		return err
	}

	if err := os.Remove(filesystem.JoinPaths(destinationDir, boltdb.EncryptedDatabaseFileName)); err != nil && !os.IsNotExist(err) {
		return err
	}

	// Now copy the database.  It'll be either portainer.db or portainer.edb

	// Note: CopyPath does not return an error if the source file doesn't exist
	if err := filesystem.CopyPath(filesystem.JoinPaths(srcDir, boltdb.EncryptedDatabaseFileName), destinationDir); err != nil {
		return err
	}

	return filesystem.CopyPath(filesystem.JoinPaths(srcDir, boltdb.DatabaseFileName), destinationDir)
}
