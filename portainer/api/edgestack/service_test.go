package edgestack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

type providerFunc func(context.Context, SummaryRequest, []Evidence) (Summary, error)

func (f providerFunc) Summarize(c context.Context, r SummaryRequest, e []Evidence) (Summary, error) {
	return f(c, r, e)
}

type fakeDispatcher struct {
	version    string
	applied    int
	err        error
	afterApply func()
}

func (f *fakeDispatcher) Preview(context.Context, Task, ModelPackage) (string, error) {
	return f.version, nil
}
func (f *fakeDispatcher) Apply(_ context.Context, _ Task, _ ModelPackage, version string) error {
	if version != f.version {
		return errors.New("stale target")
	}
	f.applied++
	if f.afterApply != nil {
		f.afterApply()
	}
	return f.err
}

type fixture struct {
	s               *Service
	handler         http.Handler
	now             time.Time
	endpointAllowed bool
	dispatcher      *fakeDispatcher
	token           string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{now: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC), endpointAllowed: true, dispatcher: &fakeDispatcher{version: "v1"}}
	var err error
	f.s, err = Open(filepath.Join(t.TempDir(), "business.db"), Options{Now: func() time.Time { return f.now }, Dispatcher: f.dispatcher, EndpointAccess: func(_ *http.Request, _ int) bool { return f.endpointAllowed }, Authenticate: func(r *http.Request) (Principal, error) {
		switch r.Header.Get("X-Test-User") {
		case "admin":
			return Principal{UserID: 1, Admin: true}, nil
		case "alice":
			return Principal{UserID: 2}, nil
		case "bob":
			return Principal{UserID: 3}, nil
		case "team":
			return Principal{UserID: 4, TeamIDs: []int{9}}, nil
		}
		return Principal{}, errors.New("unauthorized")
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.s.Close() })
	f.handler = f.s.Handler()
	for _, d := range []Device{{ID: "device-a", Name: "A", EndpointID: 1, Architecture: "amd64", ACL: ACL{UserIDs: []int{2}, TeamIDs: []int{9}}}, {ID: "device-b", Name: "B", EndpointID: 2, Architecture: "amd64", ACL: ACL{UserIDs: []int{3}}}} {
		f.ok(t, "admin", "POST", "/admin/devices", d, 200)
	}
	for _, task := range []Task{{ID: "task-a", DeviceID: "device-a", Name: "Voice", Kind: "voice", State: "running", Target: Target{EndpointID: 1, StackID: 1, ServiceID: "voice"}, ACL: ACL{UserIDs: []int{2}, TeamIDs: []int{9}}, Models: []ModelBinding{{Slot: "llm", PackageID: "old"}}, Capabilities: []string{"replace-model"}, ModelEnvKeys: []string{"MODEL_ID", "MODEL_HASH"}}, {ID: "task-b", DeviceID: "device-b", Name: "Private", Kind: "voice", Target: Target{EndpointID: 2, StackID: 2, ServiceID: "voice"}, ACL: ACL{UserIDs: []int{3}}, Models: []ModelBinding{}}} {
		f.ok(t, "admin", "POST", "/admin/tasks", task, 200)
	}
	for _, source := range []Source{{ID: "source-a", DeviceID: "device-a", TaskID: "task-a", Name: "Producer", Type: "voice.jsonl", Enabled: true}, {ID: "source-b", DeviceID: "device-b", TaskID: "task-b", Name: "Private", Type: "voice.jsonl", Enabled: true}} {
		f.ok(t, "admin", "POST", "/admin/sources", source, 200)
	}
	resp := f.ok(t, "admin", "POST", "/admin/ingest-credentials", Credential{Name: "adapter", DeviceIDs: []string{"device-a"}, TaskIDs: []string{"task-a"}, SourceIDs: []string{"source-a"}}, 201)
	f.token = resp["token"].(string)
	return f
}
func (f *fixture) request(t *testing.T, user, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var b []byte
	if body != nil {
		var err error
		b, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, "/api/edgestack"+path, bytes.NewReader(b))
	r.Header.Set("X-Test-User", user)
	if strings.HasPrefix(user, "es_ingest_") {
		r.Header.Set("Authorization", "Bearer "+user)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}
func (f *fixture) ok(t *testing.T, user, method, path string, body any, status int) map[string]any {
	t.Helper()
	// These original user-flow fixtures represent packages already published
	// before the administrator validation gate. New-gate tests use request
	// directly and never receive this trusted legacy-fixture migration.
	var legacy *ModelPackage
	if user == "admin" && method == "POST" && path == "/admin/models" && status == 200 {
		if model, ok := body.(ModelPackage); ok && model.Published {
			copy := model
			copy.Published = false
			if copy.ModelID == "" {
				copy.ModelID = "fixture-model"
			}
			body = copy
			legacy = &copy
		}
	}
	if user == "admin" && method == "POST" && strings.HasSuffix(path, "/publication") {
		raw, _ := json.Marshal(body)
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		if _, exists := p["metadataVersion"]; !exists {
			modelID := strings.TrimSuffix(strings.TrimPrefix(path, "/admin/models/"), "/publication")
			_ = f.s.db.View(func(tx *bolt.Tx) error {
				p["metadataVersion"] = f.s.modelPolicy(tx, modelID).MetadataVersion
				return nil
			})
			body = p
		}
	}
	var w *httptest.ResponseRecorder
	if legacy != nil {
		w = httptest.NewRecorder()
		respond(w, 200, *legacy)
	} else {
		w = f.request(t, user, method, path, body)
	}
	if legacy != nil && w.Code == 200 {
		err := f.s.db.Update(func(tx *bolt.Tx) error {
			legacy.Published = true
			if e := put(tx, "models", legacy.ID, *legacy); e != nil {
				return e
			}
			policy := f.s.modelPolicy(tx, legacy.ID)
			policy.LegacyScoped = true
			policy.ValidationStatus = "legacy_scoped"
			policy.LegacyTaskScopes = map[string]PublicationScope{}
			tasks, e := list[Task](tx, "tasks")
			if e != nil {
				return e
			}
			for _, task := range tasks {
				device, e := read[Device](tx, "devices", task.DeviceID)
				if e == nil && compatibleBody(task, device, *legacy) {
					policy.AllowedTaskIDs = append(policy.AllowedTaskIDs, task.ID)
					policy.LegacyTaskScopes[task.ID] = scopeFor(task, device)
				}
			}
			return put(tx, "model-policies", legacy.ID, policy)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if w.Code != status {
		t.Fatalf("%s %s got %d want %d: %s", method, path, w.Code, status, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func (f *fixture) event(id string) Event {
	return Event{SchemaVersion: 1, EventID: id, SourceID: "source-a", TaskID: "task-a", Type: "voice.user_utterance", OccurredAt: f.now.Add(-time.Hour), SessionID: "session-1", Sequence: 1, Text: "Please remind me to drink water"}
}
func (f *fixture) ingest(t *testing.T, event Event) map[string]any {
	return f.ok(t, f.token, "POST", "/ingest/events", map[string]any{"events": []Event{event}}, 200)
}

func TestAuthorizationIntersectsDeviceTaskAndOriginalEndpoint(t *testing.T) {
	f := setup(t)
	f.ok(t, "", "GET", "/devices", nil, 401)
	items := f.ok(t, "alice", "GET", "/devices", nil, 200)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != "device-a" {
		t.Fatalf("scope leak: %v", items)
	}
	f.ok(t, "alice", "GET", "/devices/device-b", nil, 404)
	f.ok(t, "alice", "GET", "/tasks/task-b/models", nil, 404)
	f.ok(t, "alice", "POST", "/admin/devices", Device{}, 403)
	f.ok(t, "team", "GET", "/devices/device-a", nil, 200)
	// Granting a device does not grant its task. Task details and events stay hidden.
	if err := f.s.db.Update(func(tx *bolt.Tx) error {
		task, e := read[Task](tx, "tasks", "task-a")
		if e != nil {
			return e
		}
		task.UserIDs = nil
		task.TeamIDs = nil
		return put(tx, "tasks", task.ID, task)
	}); err != nil {
		t.Fatal(err)
	}
	d := f.ok(t, "alice", "GET", "/devices/device-a", nil, 200)
	if len(d["tasks"].([]any)) != 0 {
		t.Fatal("task ACL bypass")
	}
	f.ok(t, "alice", "GET", "/tasks/task-a/models", nil, 404)
	f.endpointAllowed = false
	f.ok(t, "alice", "GET", "/devices/device-a", nil, 404)
	if len(f.ok(t, "alice", "GET", "/devices", nil, 200)["items"].([]any)) != 0 {
		t.Fatal("endpoint ACL bypass")
	}
}

func TestIngestIsIdempotentScopedAndRetained(t *testing.T) {
	f := setup(t)
	event := f.event("event-1")
	got := f.ingest(t, event)
	if got["accepted"] != float64(1) {
		t.Fatal(got)
	}
	got = f.ingest(t, event)
	if got["duplicates"] != float64(1) {
		t.Fatal(got)
	}
	event.Text = "changed"
	f.ok(t, f.token, "POST", "/ingest/events", map[string]any{"events": []Event{event}}, 409)
	event = f.event("event-private")
	event.SourceID = "source-b"
	event.TaskID = "task-b"
	f.ok(t, f.token, "POST", "/ingest/events", map[string]any{"events": []Event{event}}, 403)
	f.ok(t, "admin", "POST", "/ingest/events", map[string]any{"events": []Event{f.event("admin-token")}}, 401)
	rows := f.ok(t, "alice", "GET", "/events?deviceId=device-a", nil, 200)["items"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["deviceId"] != "device-a" {
		t.Fatal(rows)
	}
	status := map[string]any{"sourceId": "source-a", "taskId": "task-a", "status": "streaming", "checkpoint": map[string]any{"files": []any{map[string]any{"offset": 64}}, "queueDepth": 0}, "lastEventAt": nil, "coverage": map[string]any{"complete": false, "gapCount": 2}}
	f.ok(t, f.token, "POST", "/ingest/status", status, 200)
	sources := f.ok(t, "alice", "GET", "/devices/device-a/event-sources", nil, 200)
	coverage := sources["coverage"].(map[string]any)
	if coverage["complete"] != false {
		t.Fatal("lost source gaps")
	}
	if err := f.s.db.View(func(tx *bolt.Tx) error {
		rows, _ := list[credentialRecord](tx, "credentials")
		if len(rows) != 1 || strings.Contains(rows[0].Hash, f.token) || len(rows[0].Hash) != 64 {
			t.Fatal("credential not hashed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(8 * 24 * time.Hour)
	if err := f.s.Prune(); err != nil {
		t.Fatal(err)
	}
	rows = f.ok(t, "alice", "GET", "/events?deviceId=device-a", nil, 200)["items"].([]any)
	if len(rows) != 0 {
		t.Fatal("retention failed")
	}
}

func TestCredentialRevocationAndAtomicBatch(t *testing.T) {
	f := setup(t)
	bad := f.event("bad")
	bad.TaskID = "task-b"
	f.ok(t, f.token, "POST", "/ingest/events", map[string]any{"events": []Event{f.event("good"), bad}}, 403)
	if len(f.ok(t, "alice", "GET", "/events?deviceId=device-a", nil, 200)["items"].([]any)) != 0 {
		t.Fatal("partial batch committed")
	}
	credentials := f.ok(t, "admin", "GET", "/admin/ingest-credentials", nil, 200)["items"].([]any)
	id := credentials[0].(map[string]any)["id"].(string)
	f.ok(t, "admin", "DELETE", "/admin/ingest-credentials/"+id, nil, 200)
	f.ok(t, f.token, "POST", "/ingest/events", map[string]any{"events": []Event{f.event("revoked")}}, 401)
}

func TestSummaryNoEventsNoCloudAndEvidenceReauthorization(t *testing.T) {
	f := setup(t)
	calls := 0
	f.s.opts.Provider = providerFunc(func(_ context.Context, _ SummaryRequest, e []Evidence) (Summary, error) {
		calls++
		return Summary{Title: "Hydration", Summary: "A hydration reminder was requested.", Topics: []Topic{{Text: "Hydration reminder", EventRefs: []string{e[0].Ref}}}}, nil
	})
	request := SummaryRequest{DeviceID: "device-a", From: f.now.Add(-24 * time.Hour), To: f.now, Question: "What happened?"}
	empty := f.ok(t, "alice", "POST", "/summaries", request, 201)
	if empty["status"] != "no_data" || calls != 0 {
		t.Fatal("empty history called cloud")
	}
	f.ingest(t, f.event("evidence-1"))
	result := f.ok(t, "alice", "POST", "/summaries", request, 201)
	if result["status"] != "completed" || calls != 1 {
		t.Fatal(result)
	}
	id := result["id"].(string)
	f.ok(t, "alice", "GET", "/summaries/"+id, nil, 200)
	f.ok(t, "bob", "GET", "/summaries/"+id, nil, 404)
	f.endpointAllowed = false
	f.ok(t, "alice", "GET", "/summaries/"+id, nil, 404)
}

func TestSummaryRejectsFabricatedRefsAndRevocationDuringProviderCall(t *testing.T) {
	f := setup(t)
	f.ingest(t, f.event("evidence-1"))
	request := SummaryRequest{DeviceID: "device-a", From: f.now.Add(-24 * time.Hour), To: f.now}
	f.s.opts.Provider = providerFunc(func(context.Context, SummaryRequest, []Evidence) (Summary, error) {
		return Summary{Title: "T", Summary: "S", Topics: []Topic{{Text: "invented", EventRefs: []string{"evt:unknown"}}}}, nil
	})
	result := f.ok(t, "alice", "POST", "/summaries", request, 201)
	if result["errorCode"] != "provider_invalid_evidence" {
		t.Fatal(result)
	}
	f.s.opts.Provider = providerFunc(func(_ context.Context, _ SummaryRequest, e []Evidence) (Summary, error) {
		f.endpointAllowed = false
		return Summary{Title: "T", Summary: "S", Topics: []Topic{{Text: "T", EventRefs: []string{e[0].Ref}}}}, nil
	})
	f.ok(t, "alice", "POST", "/summaries", request, 404)
}

func TestModelPreviewConfirmationRevisionAndObservation(t *testing.T) {
	f := setup(t)
	pkg := ModelPackage{ID: "new", Name: "New model", Version: "2", Kind: "llm", Slot: "llm", TaskKinds: []string{"voice"}, Architectures: []string{"amd64"}, Published: true, Immutable: true, Image: "example/model@sha256:" + strings.Repeat("a", 64), ModelID: "new-model", ModelHash: "sha256:" + strings.Repeat("b", 64), Environment: map[string]string{"MODEL_ID": "new-model"}, RequiresRestart: true}
	f.ok(t, "admin", "POST", "/admin/models", pkg, 200)
	f.ok(t, "admin", "POST", "/admin/models", pkg, 409)
	models := f.ok(t, "alice", "GET", "/tasks/task-a/models", nil, 200)
	if models["canReplaceModel"] != true {
		t.Fatal(models)
	}
	preview := f.ok(t, "alice", "POST", "/tasks/task-a/model-change-preview", map[string]any{"packageId": "new", "slot": "llm", "revision": 1}, 200)
	confirm := map[string]any{"previewId": preview["id"], "confirm": true}
	f.ok(t, "bob", "POST", "/tasks/task-a/model-changes", confirm, 404)
	op := f.ok(t, "alice", "POST", "/tasks/task-a/model-changes", confirm, 202)
	if op["status"] != "awaiting_observation" || f.dispatcher.applied != 1 {
		t.Fatal(op)
	}
	f.ok(t, "alice", "POST", "/tasks/task-a/model-changes", confirm, 200)
	if f.dispatcher.applied != 1 {
		t.Fatal("confirmation replay dispatched twice")
	}
	f.ok(t, "alice", "POST", "/tasks/task-a/model-change-preview", map[string]any{"packageId": "new", "slot": "llm", "revision": 1}, 409)
	status := map[string]any{"sourceId": "source-a", "taskId": "task-a", "status": "connected", "observations": []any{map[string]any{"operationId": op["id"], "slot": "llm", "packageId": "new", "revision": 2, "state": "loaded", "modelId": "new-model", "modelHash": pkg.ModelHash, "observedAt": f.now}}}
	f.ok(t, f.token, "POST", "/ingest/status", status, 403)
	observer := f.ok(t, "admin", "POST", "/admin/ingest-credentials", Credential{Name: "runtime observer", DeviceIDs: []string{"device-a"}, TaskIDs: []string{"task-a"}, SourceIDs: []string{"source-a"}, Capabilities: []string{"ingest-status", "observe-model"}}, 201)["token"].(string)
	f.ok(t, observer, "POST", "/ingest/status", status, 200)
	op = f.ok(t, "alice", "GET", "/operations/"+op["id"].(string), nil, 200)
	if op["status"] != "succeeded" {
		t.Fatal(op)
	}
}

func TestModelTargetChangedAfterPreviewFailsWithoutLoadedClaim(t *testing.T) {
	f := setup(t)
	model := ModelPackage{ID: "new", Name: "New", Version: "2", Slot: "llm", TaskKinds: []string{"voice"}, Architectures: []string{"amd64"}, Published: true, Immutable: true, Image: "model@sha256:" + strings.Repeat("a", 64), ModelHash: "sha256:" + strings.Repeat("b", 64)}
	f.ok(t, "admin", "POST", "/admin/models", model, 200)
	preview := f.ok(t, "alice", "POST", "/tasks/task-a/model-change-preview", map[string]any{"packageId": "new", "slot": "llm", "revision": 1}, 200)
	f.dispatcher.version = "v2"
	op := f.ok(t, "alice", "POST", "/tasks/task-a/model-changes", map[string]any{"previewId": preview["id"], "confirm": true}, 202)
	if op["status"] != "failed" || f.dispatcher.applied != 0 {
		t.Fatal(op)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestDeepSeekRequestIsServerOnlyJSONAndBounded(t *testing.T) {
	file := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(file, []byte("test-value-never-used-on-network"), 0600); err != nil {
		t.Fatal(err)
	}
	provider := &DeepSeekProvider{SecretFile: file, Model: "deepseek-flash", Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != DeepSeekURL || r.Header.Get("Authorization") != "Bearer test-value-never-used-on-network" {
			t.Fatal("wrong endpoint or secret transport")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["max_tokens"] != float64(2048) || body["thinking"].(map[string]any)["type"] != "disabled" || body["response_format"].(map[string]any)["type"] != "json_object" {
			t.Fatal(body)
		}
		content := `{"title":"T","summary":"S","topics":[{"text":"T","eventRefs":["evt:e"]}],"answer":"","uncertainties":[]}`
		response, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
		return &http.Response{StatusCode: 200, Body: ioNop(response), Header: make(http.Header)}, nil
	})}}
	out, err := provider.Summarize(context.Background(), SummaryRequest{}, []Evidence{{Ref: "evt:e", EventID: "e", Text: "recorded"}})
	if err != nil || out.Title != "T" {
		t.Fatalf("%+v %v", out, err)
	}
	provider.Client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 401, Body: ioNop([]byte("never expose provider body")), Header: make(http.Header)}, nil
	})
	_, err = provider.Summarize(context.Background(), SummaryRequest{}, []Evidence{{Ref: "e"}})
	if providerCode(err) != "provider_authentication_failed" {
		t.Fatal(err)
	}
}

type byteReadCloser struct{ *bytes.Reader }

func (byteReadCloser) Close() error { return nil }
func ioNop(b []byte) byteReadCloser { return byteReadCloser{bytes.NewReader(b)} }

func TestPlaybackDoesNotBecomeConversationEvidence(t *testing.T) {
	f := setup(t)
	e := f.event("playback")
	e.Type = "voice.playback_state"
	e.Phase = "requested"
	e.Text = ""
	f.ingest(t, e)
	calls := 0
	f.s.opts.Provider = providerFunc(func(context.Context, SummaryRequest, []Evidence) (Summary, error) {
		calls++
		return Summary{}, fmt.Errorf("unexpected")
	})
	result := f.ok(t, "alice", "POST", "/summaries", SummaryRequest{DeviceID: "device-a", From: f.now.Add(-24 * time.Hour), To: f.now}, 201)
	if calls != 0 || result["status"] != "no_data" {
		t.Fatal(result)
	}
}

func TestHashReceiptConfirmsInitialBindingAndResolvesPendingOperation(t *testing.T) {
	f := setup(t)
	model := ModelPackage{ID: "old", Name: "Encoder 2s", Version: "2s", Kind: "asr", Slot: "llm", TaskKinds: []string{"voice"}, Architectures: []string{"amd64"}, Published: true, Immutable: true, Image: "voice@sha256:" + strings.Repeat("a", 64), ModelID: "qwen3-asr", ModelHash: "sha256:" + strings.Repeat("c", 64)}
	f.ok(t, "admin", "POST", "/admin/models", model, 200)
	observer := f.ok(t, "admin", "POST", "/admin/ingest-credentials", Credential{Name: "runtime observer", DeviceIDs: []string{"device-a"}, TaskIDs: []string{"task-a"}, SourceIDs: []string{"source-a"}, Capabilities: []string{"ingest-status", "observe-model"}}, 201)["token"].(string)
	receipt := map[string]any{"slot": "llm", "state": "loaded", "modelId": "qwen3-asr", "modelHash": "sha256:" + strings.Repeat("d", 64), "observedAt": f.now}
	status := map[string]any{"sourceId": "source-a", "taskId": "task-a", "status": "healthy", "observations": []any{receipt}}
	f.ok(t, observer, "POST", "/ingest/status", status, 409)
	receipt["modelHash"] = model.ModelHash
	f.ok(t, observer, "POST", "/ingest/status", status, 200)
	if err := f.s.db.View(func(tx *bolt.Tx) error {
		task, _ := read[Task](tx, "tasks", "task-a")
		if task.Models[0].ObservedPackageID != "old" || task.Models[0].ObservedModelHash != model.ModelHash || task.Models[0].State != "loaded" {
			t.Fatal(task.Models)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	model.ID = "new"
	model.Name = "Encoder 4s"
	model.Version = "4s"
	model.ModelHash = "sha256:" + strings.Repeat("e", 64)
	f.ok(t, "admin", "POST", "/admin/models", model, 200)
	preview := f.ok(t, "alice", "POST", "/tasks/task-a/model-change-preview", map[string]any{"packageId": "new", "slot": "llm", "revision": 1}, 200)
	op := f.ok(t, "alice", "POST", "/tasks/task-a/model-changes", map[string]any{"previewId": preview["id"], "confirm": true}, 202)
	receipt["modelHash"] = model.ModelHash
	receipt["observedAt"] = f.now.Add(-time.Minute)
	f.ok(t, observer, "POST", "/ingest/status", status, 409)
	receipt["observedAt"] = f.now
	f.ok(t, observer, "POST", "/ingest/status", status, 200)
	final := f.ok(t, "alice", "GET", "/operations/"+op["id"].(string), nil, 200)
	if final["status"] != "succeeded" {
		t.Fatal(final)
	}
}

func TestRuntimeFailureCanConfirmPreviousPublishedPackage(t *testing.T) {
	f := setup(t)
	old := ModelPackage{ID: "old", Name: "Previous", Version: "1", Slot: "llm", TaskKinds: []string{"voice"}, Architectures: []string{"amd64"}, Published: true, Immutable: true, Image: "voice@sha256:" + strings.Repeat("a", 64), ModelID: "qwen3-asr", ModelHash: "sha256:" + strings.Repeat("b", 64)}
	newModel := old
	newModel.ID = "new"
	newModel.Name = "Replacement"
	newModel.Version = "2"
	newModel.ModelHash = "sha256:" + strings.Repeat("c", 64)
	f.ok(t, "admin", "POST", "/admin/models", old, 200)
	f.ok(t, "admin", "POST", "/admin/models", newModel, 200)
	observer := f.ok(t, "admin", "POST", "/admin/ingest-credentials", Credential{Name: "runtime observer", DeviceIDs: []string{"device-a"}, TaskIDs: []string{"task-a"}, SourceIDs: []string{"source-a"}, Capabilities: []string{"ingest-status", "observe-model"}}, 201)["token"].(string)
	preview := f.ok(t, "alice", "POST", "/tasks/task-a/model-change-preview", map[string]any{"packageId": "new", "slot": "llm", "revision": 1}, 200)
	failed := f.ok(t, "alice", "POST", "/tasks/task-a/model-changes", map[string]any{"previewId": preview["id"], "confirm": true}, 202)
	f.ok(t, observer, "POST", "/ingest/status", map[string]any{"sourceId": "source-a", "taskId": "task-a", "status": "error", "observations": []any{map[string]any{"operationId": failed["id"], "slot": "llm", "packageId": "new", "revision": 2, "state": "failed"}}}, 200)
	preview = f.ok(t, "alice", "POST", "/tasks/task-a/model-change-preview", map[string]any{"packageId": "old", "slot": "llm", "revision": 2}, 200)
	recovery := f.ok(t, "alice", "POST", "/tasks/task-a/model-changes", map[string]any{"previewId": preview["id"], "confirm": true}, 202)
	if recovery["packageId"] != "old" || recovery["revision"] != float64(3) || recovery["status"] != "awaiting_observation" {
		t.Fatal(recovery)
	}
	f.ok(t, observer, "POST", "/ingest/status", map[string]any{"sourceId": "source-a", "taskId": "task-a", "status": "healthy", "observations": []any{map[string]any{"slot": "llm", "modelId": old.ModelID, "modelHash": old.ModelHash, "observedAt": f.now, "state": "loaded"}}}, 200)
	result := f.ok(t, "alice", "GET", "/operations/"+recovery["id"].(string), nil, 200)
	if result["status"] != "succeeded" {
		t.Fatal(result)
	}
}

func TestDispatchOutcomeRecordedWhenCallerAccessIsRevoked(t *testing.T) {
	for _, access := range []string{"endpoint", "session"} {
		for _, outcome := range []string{"accepted", "failed"} {
			t.Run(access+"/"+outcome, func(t *testing.T) {
				f := setup(t)
				model := ModelPackage{ID: "new", Name: "New", Version: "2", Slot: "llm", TaskKinds: []string{"voice"}, Architectures: []string{"amd64"}, Published: true, Immutable: true, Image: "new@sha256:" + strings.Repeat("a", 64), ModelID: "qwen3-asr", ModelHash: "sha256:" + strings.Repeat("b", 64)}
				f.ok(t, "admin", "POST", "/admin/models", model, 200)
				preview := f.ok(t, "alice", "POST", "/tasks/task-a/model-change-preview", map[string]any{"packageId": "new", "slot": "llm", "revision": 1}, 200)
				revoked := false
				authenticate := f.s.opts.Authenticate
				if access == "session" {
					f.s.opts.Authenticate = func(r *http.Request) (Principal, error) {
						if revoked {
							return Principal{}, errors.New("session revoked")
						}
						return authenticate(r)
					}
				}
				f.dispatcher.afterApply = func() {
					if access == "session" {
						revoked = true
					} else {
						f.endpointAllowed = false
					}
				}
				if outcome == "failed" {
					f.dispatcher.err = errors.New("deployment metadata failed")
				}
				expectedHTTP := 404
				if access == "session" {
					expectedHTTP = 401
				}
				confirm := map[string]any{"previewId": preview["id"], "confirm": true}
				response := f.ok(t, "alice", "POST", "/tasks/task-a/model-changes", confirm, expectedHTTP)
				if _, leaked := response["taskId"]; leaked {
					t.Fatal("revoked caller received task details")
				}
				var operation Operation
				if err := f.s.db.View(func(tx *bolt.Tx) error {
					record, e := read[previewRecord](tx, "previews", preview["id"].(string))
					if e != nil {
						return e
					}
					operation, e = read[Operation](tx, "operations", record.Preview.OperationID)
					if e != nil {
						return e
					}
					task, e := read[Task](tx, "tasks", "task-a")
					if e != nil {
						return e
					}
					if outcome == "accepted" {
						if operation.Status != "awaiting_observation" || task.Revision != 2 || task.Models[0].PackageID != "new" {
							t.Fatalf("successful dispatch was not recorded: %+v %+v", operation, task)
						}
					} else if operation.Status != "failed" || operation.ErrorCode != "deployment_failed" || task.Revision != 1 || task.Models[0].PackageID != "old" {
						t.Fatalf("failed dispatch was not recorded: %+v %+v", operation, task)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				f.ok(t, "alice", "GET", "/operations/"+operation.ID, nil, expectedHTTP)
				revoked = false
				f.endpointAllowed = true
				f.dispatcher.afterApply = nil
				f.ok(t, "alice", "POST", "/tasks/task-a/model-changes", confirm, 200)
				if f.dispatcher.applied != 1 {
					t.Fatal("retry after restored authorization dispatched twice")
				}
			})
		}
	}
}
