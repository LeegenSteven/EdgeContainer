package edgestack

import (
	"context"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func TestPublicationWithdrawalBlocksNewSelectionWithoutRevivingOnBootstrap(t *testing.T) {
	f := setup(t)
	model := ModelPackage{ID: "new", Name: "New", Version: "2", Slot: "llm", TaskKinds: []string{"voice"}, Architectures: []string{"amd64"}, Published: true, Immutable: true, Image: "new@sha256:" + strings.Repeat("a", 64), ModelID: "qwen3-asr", ModelHash: "sha256:" + strings.Repeat("b", 64)}
	f.ok(t, "admin", "POST", "/admin/models", model, 200)
	preview := f.ok(t, "alice", "POST", "/tasks/task-a/model-change-preview", map[string]any{"packageId": "new", "slot": "llm", "revision": 1}, 200)
	f.ok(t, "alice", "POST", "/admin/models/new/publication", map[string]bool{"published": false}, 403)
	f.ok(t, "admin", "POST", "/admin/models/new/publication", map[string]bool{"published": false}, 200)
	models := f.ok(t, "alice", "GET", "/tasks/task-a/models", nil, 200)
	if len(models["items"].([]any)) != 0 {
		t.Fatal("withdrawn package remained selectable")
	}
	confirm := map[string]any{"previewId": preview["id"], "confirm": true}
	f.ok(t, "alice", "POST", "/tasks/task-a/model-changes", confirm, 409)
	if f.dispatcher.applied != 0 {
		t.Fatal("withdrawn preview dispatched")
	}
	if err := f.s.Bootstrap(context.Background(), BootstrapConfig{Models: []ModelPackage{model}}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.db.View(func(tx *bolt.Tx) error {
		stored, err := read[ModelPackage](tx, "models", model.ID)
		if stored.Published {
			t.Fatal("startup file revived withdrawn package")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	f.ok(t, "admin", "POST", "/admin/models/new/publication", map[string]bool{"published": true}, 200)
	models = f.ok(t, "alice", "GET", "/tasks/task-a/models", nil, 200)
	if len(models["items"].([]any)) != 1 {
		t.Fatal("administrator could not republish package")
	}
	op := f.ok(t, "alice", "POST", "/tasks/task-a/model-changes", confirm, 202)
	if f.dispatcher.applied != 1 {
		t.Fatal(op)
	}
	// Withdrawal controls future selection, never the truth of the current
	// runtime. A dispatched package can still report its actual loaded hash.
	f.ok(t, "admin", "POST", "/admin/models/new/publication", map[string]bool{"published": false}, 200)
	observer := f.ok(t, "admin", "POST", "/admin/ingest-credentials", Credential{Name: "observer", DeviceIDs: []string{"device-a"}, TaskIDs: []string{"task-a"}, SourceIDs: []string{"source-a"}, Capabilities: []string{"ingest-status", "observe-model"}}, 201)["token"].(string)
	f.ok(t, observer, "POST", "/ingest/status", map[string]any{"sourceId": "source-a", "taskId": "task-a", "status": "healthy", "observations": []any{map[string]any{"slot": "llm", "modelId": model.ModelID, "modelHash": model.ModelHash, "observedAt": f.now, "state": "loaded"}}}, 200)
	result := f.ok(t, "alice", "GET", "/operations/"+op["id"].(string), nil, 200)
	if result["status"] != "succeeded" {
		t.Fatal("withdrawal hid true loaded receipt", result)
	}
	if err := f.s.db.View(func(tx *bolt.Tx) error {
		task, err := read[Task](tx, "tasks", "task-a")
		if task.State != "running" || task.Models[0].State != "loaded" || task.Models[0].ObservedPackageID != "new" {
			t.Fatal("withdrawal stopped or falsified current runtime", task)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	altered := model
	altered.ModelHash = "sha256:" + strings.Repeat("c", 64)
	if err := f.s.Bootstrap(context.Background(), BootstrapConfig{Models: []ModelPackage{altered}}); err == nil {
		t.Fatal("publication exception allowed immutable package mutation")
	}
}

func TestPublicationBodyIsNarrowAndRequired(t *testing.T) {
	f := setup(t)
	f.ok(t, "admin", "POST", "/admin/models/unknown/publication", map[string]any{}, 400)
	f.ok(t, "admin", "POST", "/admin/models/unknown/publication", map[string]any{"published": true, "image": "untrusted"}, 400)
	f.ok(t, "admin", "POST", "/admin/models/unknown/publication", map[string]any{"published": true}, 404)
}
