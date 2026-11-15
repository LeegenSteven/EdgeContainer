package edgestack

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func TestAdminMetadataCASPreservesModelsTargetsAndSourceEvidence(t *testing.T) {
	f := setup(t)
	observed := f.now.Add(-time.Hour)
	if err := f.s.db.Update(func(tx *bolt.Tx) error {
		task, _ := read[Task](tx, "tasks", "task-a")
		task.Models[0].State = "loaded"
		task.Models[0].ObservedPackageID = "old"
		task.Models[0].ObservedAt = observed
		if e := put(tx, "tasks", task.ID, task); e != nil {
			return e
		}
		source, _ := read[Source](tx, "sources", "source-a")
		source.Status = "streaming"
		source.Checkpoint = json.RawMessage(`{"offset":128}`)
		source.LastSeenAt = f.now
		return put(tx, "sources", source.ID, source)
	}); err != nil {
		t.Fatal(err)
	}
	f.s.opts.ValidateACL = func(acl ACL) error {
		for _, id := range acl.UserIDs {
			if id != 2 && id != 3 {
				return errors.New("unknown user")
			}
		}
		for _, id := range acl.TeamIDs {
			if id != 9 {
				return errors.New("unknown team")
			}
		}
		return nil
	}
	before := f.ok(t, "admin", "GET", "/admin/tasks/task-a", nil, 200)
	updated := f.ok(t, "admin", "PATCH", "/admin/tasks/task-a", map[string]any{"metadataVersion": before["metadataVersion"], "name": "Renamed", "description": "Friendly purpose", "userIds": []int{2}}, 200)
	if updated["metadataVersion"] != float64(2) || updated["revision"] != float64(1) || updated["models"].([]any)[0].(map[string]any)["state"] != "loaded" {
		t.Fatal("metadata edit rewrote deployment/observation", updated)
	}
	f.ok(t, "admin", "PATCH", "/admin/tasks/task-a", map[string]any{"metadataVersion": 1, "name": "Stale"}, 409)
	f.ok(t, "admin", "PATCH", "/admin/tasks/task-a", map[string]any{"metadataVersion": 2, "target": Target{EndpointID: 1, StackID: 99, ServiceID: "voice"}}, 400)
	f.ok(t, "admin", "POST", "/admin/tasks", Task{ID: "task-a"}, 409)
	f.ok(t, "admin", "PATCH", "/admin/tasks/task-a", map[string]any{"metadataVersion": 2, "userIds": []int{999}}, 400)
	f.ok(t, "alice", "PATCH", "/admin/tasks/task-a", map[string]any{"metadataVersion": 2, "name": "forbidden"}, 403)
	source := f.ok(t, "admin", "PATCH", "/admin/sources/source-a", map[string]any{"metadataVersion": 1, "name": "Renamed source", "enabled": false}, 200)
	if source["status"] != "streaming" || source["checkpoint"].(map[string]any)["offset"] != float64(128) || source["lastSeenAt"] == nil {
		t.Fatal("source evidence was reset", source)
	}
	f.ok(t, "admin", "PATCH", "/admin/sources/source-a", map[string]any{"metadataVersion": 2, "type": "camera.observation"}, 400)
	page := f.ok(t, "admin", "GET", "/admin/tasks?page=1&pageSize=1&deviceId=device-a&q=renamed", nil, 200)
	if page["totalCount"] != float64(1) || len(page["items"].([]any)) != 1 {
		t.Fatal(page)
	}
	f.ok(t, "admin", "GET", "/admin/tasks?page=999999999999999999", nil, 400)
	f.s.opts.InspectAccess = func(user, endpoint int) (Principal, bool, error) { return Principal{UserID: user}, false, nil }
	access := f.ok(t, "admin", "GET", "/admin/access-preview?userId=2&deviceId=device-a&taskId=task-a", nil, 200)
	if access["deviceBusinessAccess"] != true || access["taskBusinessAccess"] != true || access["effectiveAccess"] != false {
		t.Fatal(access)
	}
}

func TestStartupSeedPreservesWebChangesAndImportRequiresPreviewVersion(t *testing.T) {
	f := setup(t)
	var config BootstrapConfig
	if err := f.s.db.View(func(tx *bolt.Tx) error {
		devices, e := list[Device](tx, "devices")
		if e != nil {
			return e
		}
		tasks, e := list[Task](tx, "tasks")
		if e != nil {
			return e
		}
		sources, e := list[Source](tx, "sources")
		if e != nil {
			return e
		}
		config = BootstrapConfig{Devices: devices, Tasks: tasks, Sources: sources}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.ok(t, "admin", "PATCH", "/admin/devices/device-a", map[string]any{"metadataVersion": 1, "name": "Web name", "userIds": []int{3}}, 200)
	f.ok(t, "admin", "PATCH", "/admin/sources/source-a", map[string]any{"metadataVersion": 1, "enabled": false}, 200)
	if err := f.s.Bootstrap(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	device := f.ok(t, "admin", "GET", "/admin/devices/device-a", nil, 200)
	source := f.ok(t, "admin", "GET", "/admin/sources/source-a", nil, 200)
	if device["name"] != "Web name" || source["enabled"] != false {
		t.Fatal("old startup file overwrote web state")
	}
	for i := range config.Devices {
		if config.Devices[i].ID == "device-a" {
			config.Devices[i].Name = "Imported name"
			config.Devices[i].UserIDs = []int{2}
		}
	}
	preview := f.ok(t, "admin", "POST", "/admin/imports/preview", map[string]any{"config": config}, 200)
	if f.ok(t, "admin", "GET", "/admin/devices/device-a", nil, 200)["name"] != "Web name" {
		t.Fatal("preview applied changes")
	}
	f.ok(t, "admin", "PATCH", "/admin/devices/device-a", map[string]any{"metadataVersion": 2, "description": "concurrent edit"}, 200)
	f.ok(t, "admin", "POST", "/admin/imports/confirm", map[string]any{"previewId": preview["id"], "confirm": true}, 409)
	preview = f.ok(t, "admin", "POST", "/admin/imports/preview", map[string]any{"config": config}, 200)
	confirm := map[string]any{"previewId": preview["id"], "confirm": true}
	f.ok(t, "admin", "POST", "/admin/imports/confirm", confirm, 200)
	version := f.ok(t, "admin", "GET", "/admin/devices/device-a", nil, 200)["metadataVersion"]
	f.ok(t, "admin", "POST", "/admin/imports/confirm", confirm, 200)
	device = f.ok(t, "admin", "GET", "/admin/devices/device-a", nil, 200)
	if device["name"] != "Imported name" || device["metadataVersion"] != version {
		t.Fatal("import confirmation was not idempotent")
	}
	config.Tasks[0].Target.StackID = 999
	f.ok(t, "admin", "POST", "/admin/imports/preview", map[string]any{"config": config}, 409)
}

func TestCredentialRotationTracksSuccessfulUseAndAuditsNoToken(t *testing.T) {
	f := setup(t)
	rows := f.ok(t, "admin", "GET", "/admin/ingest-credentials?deviceId=device-a&pageSize=10", nil, 200)["items"].([]any)
	if len(rows) != 1 {
		t.Fatal(rows)
	}
	old := rows[0].(map[string]any)["id"].(string)
	rotated := f.ok(t, "admin", "POST", "/admin/ingest-credentials/"+old+"/rotate", map[string]any{"name": "Replacement"}, 201)
	next := rotated["id"].(string)
	token := rotated["token"].(string)
	f.ok(t, "admin", "DELETE", "/admin/ingest-credentials/"+old+"?replacementId="+next, nil, 409)
	f.ok(t, token, "POST", "/ingest/status", map[string]any{"sourceId": "source-a", "taskId": "task-a", "status": "streaming"}, 200)
	f.ok(t, "admin", "DELETE", "/admin/ingest-credentials/"+old+"?replacementId="+next, nil, 200)
	f.ok(t, f.token, "POST", "/ingest/events", map[string]any{"events": []Event{f.event("revoked-old")}}, 401)
	rows = f.ok(t, "admin", "GET", "/admin/ingest-credentials?sourceId=source-a", nil, 200)["items"].([]any)
	used := false
	for _, raw := range rows {
		item := raw.(map[string]any)
		if item["id"] == next {
			used = item["createdByUserId"] == float64(1) && item["rotatedFromId"] == old && item["lastSuccessfulAt"] != nil
		}
	}
	if !used {
		t.Fatal("credential usage metadata absent")
	}
	audit := f.ok(t, "admin", "GET", "/admin/audit?actorUserId=1", nil, 200)
	encoded, _ := json.Marshal(audit)
	if strings.Contains(string(encoded), token) || strings.Contains(string(encoded), f.token) {
		t.Fatal("audit contained a bearer token")
	}
}

func TestDraftPackageNeedsIndependentTestAndActualReceiptBeforeScopedPublication(t *testing.T) {
	f := setup(t)
	model := ModelPackage{ID: "verified-new", Name: "New model", Version: "1", Kind: "asr", Slot: "llm", TaskKinds: []string{"voice"}, Architectures: []string{"amd64"}, RuntimeBackend: "rknn", TemplateIDs: []string{"voice-template"}, Immutable: true, Image: "example/model@sha256:" + strings.Repeat("a", 64), ModelID: "qwen3-asr", ModelHash: "sha256:" + strings.Repeat("b", 64)}
	invalid := model
	invalid.Published = true
	if w := f.request(t, "admin", "POST", "/admin/models", invalid); w.Code != 400 {
		t.Fatalf("package self-published: %d", w.Code)
	}
	f.ok(t, "admin", "POST", "/admin/models", model, 200)
	if w := f.request(t, "admin", "POST", "/admin/models/verified-new/publication", map[string]any{"metadataVersion": 1, "published": true}); w.Code != 409 {
		t.Fatal("unvalidated draft published")
	}
	f.ok(t, "admin", "POST", "/admin/models/verified-new/validation-preview", map[string]any{"taskId": "task-a", "slot": "llm", "revision": 1}, 409)
	task := f.ok(t, "admin", "PATCH", "/admin/tasks/task-a", map[string]any{"metadataVersion": 1, "testTask": true, "runtimeBackend": "rknn", "templateId": "voice-template"}, 200)
	preview := f.ok(t, "admin", "POST", "/admin/models/verified-new/validation-preview", map[string]any{"taskId": "task-a", "slot": "llm", "revision": task["revision"]}, 200)
	f.ok(t, "alice", "POST", "/admin/models/verified-new/validations", map[string]any{"previewId": preview["id"], "confirm": true}, 403)
	f.ok(t, "admin", "POST", "/tasks/task-a/model-changes", map[string]any{"previewId": preview["id"], "confirm": true}, 409)
	op := f.ok(t, "admin", "POST", "/admin/models/verified-new/validations", map[string]any{"previewId": preview["id"], "confirm": true}, 202)
	if op["purpose"] != "model-validation" || op["actorUserId"] != float64(1) || op["validationId"] != op["id"] {
		t.Fatal(op)
	}
	policy := f.ok(t, "admin", "GET", "/admin/models/verified-new", nil, 200)
	if policy["validationStatus"] != "validating" {
		t.Fatal(policy)
	}
	if w := f.request(t, "admin", "POST", "/admin/models/verified-new/publication", map[string]any{"metadataVersion": policy["metadataVersion"], "published": true}); w.Code != 409 {
		t.Fatal("dispatch alone passed validation")
	}
	observer := f.ok(t, "admin", "POST", "/admin/ingest-credentials", Credential{Name: "runtime", DeviceIDs: []string{"device-a"}, TaskIDs: []string{"task-a"}, SourceIDs: []string{"source-a"}, Capabilities: []string{"ingest-status", "observe-model"}}, 201)["token"].(string)
	receipt := map[string]any{"sourceId": "source-a", "taskId": "task-a", "status": "healthy", "observations": []any{map[string]any{"slot": "llm", "modelId": model.ModelID, "modelHash": model.ModelHash, "observedAt": f.now, "state": "loaded"}}}
	// A hash-only receipt cannot validate a package without independent proof of
	// the pinned runtime image, including a new image containing identical weights.
	f.ok(t, observer, "POST", "/ingest/status", receipt, 503)
	activeImage := "example/model@sha256:" + strings.Repeat("c", 64)
	proofCalls := 0
	f.s.opts.VerifyModelRuntime = func(ctx context.Context, observedTask Task, observedPackage ModelPackage) error {
		proofCalls++
		if observedTask.ID != "task-a" || observedPackage.ID != model.ID || ctx.Err() != nil {
			t.Fatal("runtime verifier received the wrong target")
		}
		// A writable transaction here also checks that the callback is not called
		// from inside the ingest read/write transaction.
		if err := f.s.db.Update(func(tx *bolt.Tx) error {
			return tx.Bucket([]byte("admin-meta")).Put([]byte("runtime-proof-test"), []byte("checked"))
		}); err != nil {
			return err
		}
		if activeImage != observedPackage.Image {
			return errors.New("different runtime image")
		}
		return nil
	}
	f.ok(t, observer, "POST", "/ingest/status", receipt, 503)
	f.ok(t, f.token, "POST", "/ingest/status", receipt, 403)
	if proofCalls != 1 {
		t.Fatal("unscoped credential queried runtime")
	}
	if waiting := f.ok(t, "admin", "GET", "/admin/operations/"+op["id"].(string), nil, 200); waiting["status"] != "awaiting_observation" {
		t.Fatal("mismatched image finished validation", waiting)
	}
	if p := f.ok(t, "admin", "GET", "/admin/models/verified-new", nil, 200); p["validationStatus"] != "validating" {
		t.Fatal("mismatched image produced publication evidence", p)
	}
	activeImage = model.Image
	verify := f.s.opts.VerifyModelRuntime
	f.s.opts.VerifyModelRuntime = func(ctx context.Context, task Task, m ModelPackage) error {
		if err := verify(ctx, task, m); err != nil {
			return err
		}
		return f.s.db.Update(func(tx *bolt.Tx) error {
			current, err := read[Task](tx, "tasks", task.ID)
			if err != nil {
				return err
			}
			current.Revision++
			return put(tx, "tasks", current.ID, current)
		})
	}
	f.ok(t, observer, "POST", "/ingest/status", receipt, 503)
	if p := f.ok(t, "admin", "GET", "/admin/models/verified-new", nil, 200); p["validationStatus"] != "validating" {
		t.Fatal("stale runtime proof passed validation", p)
	}
	if err := f.s.db.Update(func(tx *bolt.Tx) error {
		current, err := read[Task](tx, "tasks", "task-a")
		if err != nil {
			return err
		}
		current.Revision--
		return put(tx, "tasks", current.ID, current)
	}); err != nil {
		t.Fatal(err)
	}
	f.s.opts.VerifyModelRuntime = verify
	f.ok(t, observer, "POST", "/ingest/status", receipt, 200)
	if proofCalls != 3 {
		t.Fatal("unexpected runtime verifier calls", proofCalls)
	}
	policy = f.ok(t, "admin", "GET", "/admin/models/verified-new", nil, 200)
	if policy["validationStatus"] != "passed" {
		t.Fatal(policy)
	}
	if w := f.request(t, "admin", "POST", "/admin/models/verified-new/publication", map[string]any{"metadataVersion": policy["metadataVersion"], "published": true}); w.Code != 200 {
		t.Fatalf("trusted validation could not publish: %s", w.Body.String())
	}
	models := f.ok(t, "alice", "GET", "/tasks/task-a/models", nil, 200)
	if len(models["items"].([]any)) != 1 {
		t.Fatal(models)
	}
	validations := f.ok(t, "admin", "GET", "/admin/models/verified-new/validations", nil, 200)
	if validations["totalCount"] != float64(1) || validations["items"].([]any)[0].(map[string]any)["status"] != "passed" {
		t.Fatal(validations)
	}
	ops := f.ok(t, "admin", "GET", "/admin/operations?taskId=task-a&actorUserId=1", nil, 200)
	if ops["totalCount"] != float64(1) {
		t.Fatal(ops)
	}
}

func TestAdminMigrationDefaultsArchivedWithoutChangingProducerEnablement(t *testing.T) {
	f := setup(t)
	if err := f.s.db.Update(func(tx *bolt.Tx) error {
		for _, kind := range []string{"devices", "tasks", "sources"} {
			rows, err := list[map[string]any](tx, kind)
			if err != nil {
				return err
			}
			for _, row := range rows {
				delete(row, "archived")
				if err := put(tx, kind, row["id"].(string), row); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.migrateAdminSchema(); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"devices", "tasks", "sources"} {
		current := f.ok(t, "admin", "GET", "/admin/"+kind+"?archived=false", nil, 200)
		if current["totalCount"] != float64(2) {
			t.Fatalf("missing current %s after migration: %v", kind, current)
		}
	}
	if source := f.ok(t, "admin", "GET", "/admin/sources/source-a", nil, 200); source["enabled"] != true {
		t.Fatal("migration changed actual producer enablement")
	}
}

func TestLegacyModelMigrationFreezesOriginalTaskScopeWithoutInventingProof(t *testing.T) {
	f := setup(t)
	path := f.s.db.Path()
	options := f.s.opts
	model := ModelPackage{ID: "legacy", Name: "Legacy", Version: "1", Slot: "llm", TaskKinds: []string{"voice"}, Architectures: []string{"amd64"}, Immutable: true, Published: true, Image: "example/legacy@sha256:" + strings.Repeat("a", 64), ModelID: "legacy-model", ModelHash: "sha256:" + strings.Repeat("b", 64)}
	if err := f.s.db.Update(func(tx *bolt.Tx) error {
		if e := put(tx, "models", model.ID, model); e != nil {
			return e
		}
		return tx.Bucket([]byte("admin-meta")).Delete([]byte("schema"))
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	f.s, err = Open(filepath.Clean(path), options)
	if err != nil {
		t.Fatal(err)
	}
	f.handler = f.s.Handler()
	policy := f.ok(t, "admin", "GET", "/admin/models/legacy", nil, 200)
	if policy["legacyScoped"] != true || policy["validationStatus"] != "legacy_scoped" || policy["lastValidationId"] != nil {
		t.Fatal(policy)
	}
	f.ok(t, "admin", "POST", "/admin/tasks", Task{ID: "task-new", DeviceID: "device-a", Name: "New task", Kind: "voice", Target: Target{EndpointID: 1, StackID: 99, ServiceID: "voice"}, ACL: ACL{UserIDs: []int{2}}, Models: []ModelBinding{{Slot: "llm", PackageID: "old"}}, Capabilities: []string{"replace-model"}}, 200)
	if items := f.ok(t, "alice", "GET", "/tasks/task-new/models", nil, 200)["items"].([]any); len(items) != 0 {
		t.Fatal("legacy migration widened to a new task")
	}
	if items := f.ok(t, "alice", "GET", "/tasks/task-a/models", nil, 200)["items"].([]any); len(items) != 1 {
		t.Fatal("legacy migration lost original scope")
	}
	if total := f.ok(t, "admin", "GET", "/admin/models/legacy/validations", nil, 200)["totalCount"]; total != float64(0) {
		t.Fatal("legacy migration invented validation history")
	}
	f.ok(t, "admin", "PATCH", "/admin/tasks/task-a", map[string]any{"metadataVersion": 1, "testTask": true, "runtimeBackend": "rknn", "templateId": "new-template"}, 200)
	if items := f.ok(t, "alice", "GET", "/tasks/task-a/models", nil, 200)["items"].([]any); len(items) != 0 {
		t.Fatal("legacy scope widened after backend/template metadata change")
	}
	f.ok(t, "admin", "PATCH", "/admin/tasks/task-a", map[string]any{"metadataVersion": 2, "testTask": false, "runtimeBackend": "", "templateId": ""}, 200)
	if items := f.ok(t, "alice", "GET", "/tasks/task-a/models", nil, 200)["items"].([]any); len(items) != 1 {
		t.Fatal("restoring original metadata did not restore original legacy eligibility")
	}
}

func TestAuditRetainedNinetyDaysWithoutEventText(t *testing.T) {
	f := setup(t)
	if err := f.s.RecordAudit(nil, "system.test.failed", "models", "model", []string{"published"}); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(8 * 24 * time.Hour)
	if err := f.s.Prune(); err != nil {
		t.Fatal(err)
	}
	page := f.ok(t, "admin", "GET", "/admin/audit?result=failed", nil, 200)
	if page["totalCount"] != float64(1) {
		t.Fatal("audit incorrectly used seven-day event retention")
	}
	f.now = f.now.Add(90 * 24 * time.Hour)
	if err := f.s.Prune(); err != nil {
		t.Fatal(err)
	}
	if page := f.ok(t, "admin", "GET", "/admin/audit", nil, 200); page["totalCount"] != float64(0) {
		t.Fatal("audit retention failed")
	}
}
