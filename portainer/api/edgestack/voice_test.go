package edgestack

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func TestVoiceProxyScopesAndStripsBrowserCredentials(t *testing.T) {
	f := setup(t)
	secretFile := filepath.Join(t.TempDir(), "voice-token")
	if err := os.WriteFile(secretFile, []byte("test-proxy-upstream-only"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "Bearer test-proxy-upstream-only" || r.Header.Get("X-API-Key") != "" {
			t.Error("browser credentials forwarded")
		}
		if r.URL.Path != "/health" {
			t.Error("incorrect upstream path")
		}
		w.Header().Set("Set-Cookie", "upstream=must-not-reach-browser")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	if err := f.s.db.Update(func(tx *bolt.Tx) error {
		task, e := read[Task](tx, "tasks", "task-a")
		if e != nil {
			return e
		}
		task.VoiceOrigin = upstream.URL
		task.VoiceTokenFile = secretFile
		task.Capabilities = append(task.Capabilities, "voice-test")
		return put(tx, "tasks", task.ID, task)
	}); err != nil {
		t.Fatal(err)
	}
	f.s.opts.VoiceTestHTML = []byte(`<html><style>body{}</style><script>window.test=true;</script></html>`)
	page := f.request(t, "alice", "GET", "/tasks/task-a/voice-test", nil)
	if page.Code != 200 || !strings.Contains(page.Header().Get("Content-Security-Policy"), "'sha256-") {
		t.Fatalf("page CSP: %v %s", page.Header(), page.Body.String())
	}
	r := httptest.NewRequest("GET", "/api/edgestack/tasks/task-a/voice/health", nil)
	r.Header.Set("X-Test-User", "alice")
	r.Header.Set("Cookie", "session=private")
	r.Header.Set("Authorization", "Bearer private")
	r.Header.Set("X-API-Key", "private")
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("Set-Cookie") != "" || calls != 1 {
		t.Fatalf("proxy failed %d %v", w.Code, w.Header())
	}
	f.ok(t, "bob", "GET", "/tasks/task-a/voice/health", nil, 404)
	f.ok(t, "alice", "GET", "/tasks/task-a/voice/admin", nil, 404)
	r = httptest.NewRequest("GET", "/api/edgestack/tasks/task-a/voice/v2v/stream", nil)
	r.Header.Set("X-Test-User", "alice")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != 403 || calls != 1 {
		t.Fatal("cross-origin WebSocket reached upstream")
	}
	if err := os.Remove(secretFile); err != nil {
		t.Fatal(err)
	}
	f.ok(t, "alice", "GET", "/tasks/task-a/voice/health", nil, 503)
	if calls != 1 {
		t.Fatal("missing operator secret reached upstream")
	}
}

func TestBootstrapPreservesSourceCheckpointObservedModelsAndRevocation(t *testing.T) {
	f := setup(t)
	f.ingest(t, f.event("bootstrap-event"))
	f.ok(t, f.token, "POST", "/ingest/status", map[string]any{"sourceId": "source-a", "taskId": "task-a", "status": "streaming", "checkpoint": map[string]any{"offset": 123}}, 200)
	var config BootstrapConfig
	if err := f.s.db.View(func(tx *bolt.Tx) error {
		devices, _ := list[Device](tx, "devices")
		tasks, _ := list[Task](tx, "tasks")
		sources, _ := list[Source](tx, "sources")
		config.Devices = devices
		config.Tasks = tasks
		config.Sources = sources
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(f.token))
	config.Credentials = []BootstrapCredential{{Credential: Credential{ID: "bootstrap-token", Name: "adapter", DeviceIDs: []string{"device-a"}, TaskIDs: []string{"task-a"}, SourceIDs: []string{"source-a"}, Capabilities: []string{"ingest-events", "ingest-status"}}, TokenHash: hex.EncodeToString(hash[:])}}
	for i := 0; i < 2; i++ {
		if err := f.s.Bootstrap(context.Background(), config); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.s.db.View(func(tx *bolt.Tx) error {
		task, _ := read[Task](tx, "tasks", "task-a")
		source, _ := read[Source](tx, "sources", "source-a")
		if task.Revision != 1 || source.Status != "streaming" || string(source.Checkpoint) != `{"offset":123}` || source.LastEventAt.IsZero() {
			t.Fatalf("state reset: %+v %+v", task, source)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.ok(t, "admin", "DELETE", "/admin/ingest-credentials/bootstrap-token", nil, 200)
	if err := f.s.Bootstrap(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	if err := f.s.db.View(func(tx *bolt.Tx) error {
		c, _ := read[credentialRecord](tx, "credentials", "bootstrap-token")
		if !c.Credential.Revoked {
			t.Fatal("restart unrevoked operator token")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
