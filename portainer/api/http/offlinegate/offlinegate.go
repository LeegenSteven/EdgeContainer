package offlinegate

import (
	"errors"
	"net/http"
	"strings"
	"time"

	httperror "github.com/portainer/portainer/pkg/libhttp/error"

	"github.com/rs/zerolog/log"
	lock "github.com/viney-shih/go-lock"
)

// OfflineGate is an entity that works similar to a mutex with signaling
// Only the caller that has Locked a gate can unlock it, otherwise it will be blocked with a call to Lock.
// Gate provides a passthrough http middleware that will wait for a locked gate to be unlocked.
// For safety reasons, the middleware will timeout
type OfflineGate struct {
	lock *lock.CASMutex
}

// NewOfflineGate creates a new gate
func NewOfflineGate() *OfflineGate {
	return &OfflineGate{
		lock: lock.NewCASMutex(),
	}
}

// Lock locks readonly gate and returns a function to unlock
func (o *OfflineGate) Lock() func() {
	o.lock.Lock()
	return o.lock.Unlock
}

// ReadLease coordinates non-HTTP database writers with backup and restore.
// HTTP mutations already hold a lease through WaitingMiddleware; they must not
// acquire a second lease while a writer is waiting.
func (o *OfflineGate) ReadLease(timeout time.Duration) (func(), error) {
	if !o.lock.RTryLockWithTimeout(timeout) {
		return nil, errors.New("timed out waiting for backup or restore")
	}
	return o.lock.RUnlock, nil
}

// WaitingMiddleware returns an http handler that waits for the gate to be unlocked before continuing
func (o *OfflineGate) WaitingMiddleware(timeout time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" || r.Method == "HEAD" || r.Method == "OPTIONS" || strings.HasPrefix(r.URL.Path, "/api/backup") || strings.HasPrefix(r.URL.Path, "/api/restore") {
			next.ServeHTTP(w, r)
			return
		}

		if !o.lock.RTryLockWithTimeout(timeout) {
			log.Error().Str("url", r.URL.Path).Msg("request timed out while waiting for the backup process to finish")
			httperror.WriteError(w, http.StatusRequestTimeout, "Request timed out while waiting for the backup process to finish", http.ErrHandlerTimeout)
			return
		}

		defer o.lock.RUnlock()

		next.ServeHTTP(w, r)
	})
}
