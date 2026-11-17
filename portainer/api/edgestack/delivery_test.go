package edgestack

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func deliveryFixture(t *testing.T) (*fixture, string) {
	t.Helper()
	f := setup(t)
	token := strings.Repeat("worker-secret-", 4)
	file := filepath.Join(t.TempDir(), "worker-token")
	if err := os.WriteFile(file, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	f.s.opts.DeliveryEnabled = true
	f.s.opts.DeliveryWorkerTokenFile = file
	f.s.opts.InspectAccess = func(user, endpoint int) (Principal, bool, error) {
		return Principal{UserID: user, Admin: user == 1}, f.endpointAllowed && (user == 1 || (user == 2 && endpoint == 1) || (user == 3 && endpoint == 2)), nil
	}
	if err := f.s.db.Update(func(tx *bolt.Tx) error {
		task, e := read[Task](tx, "tasks", "task-a")
		if e != nil {
			return e
		}
		task.TestTask = true
		task.RuntimeBackend = "rknn"
		task.TemplateID = "test-template"
		if e = put(tx, "tasks", task.ID, task); e != nil {
			return e
		}
		source, e := read[Source](tx, "sources", "source-a")
		if e != nil {
			return e
		}
		source.Type = "model.observer"
		return put(tx, "sources", source.ID, source)
	}); err != nil {
		t.Fatal(err)
	}
	recipe := DeliveryRecipe{ID: "test-recipe", MetadataVersion: 0, Version: "1", Enabled: true, Engine: "rknn", TaskKind: "voice", Architecture: "amd64", RuntimeBackend: "rknn", TemplateID: "test-template", Slot: "llm", BaseImage: "example/runtime@sha256:" + strings.Repeat("a", 64), RuntimeProfileDigest: "sha256:" + strings.Repeat("c", 64), SourcePrefixes: []string{"https://models.example/"}, ValidationTaskID: "task-a", ValidationSourceID: "source-a", ModelEnvironment: map[string]string{}, Validation: DeliveryGate{MaxLatencyMs: 100, MaxMemoryBytes: 1 << 30, MaxErrorRate: 0, MinQuality: .9}}
	f.ok(t, "admin", "PUT", "/admin/delivery/recipes/"+recipe.ID, recipe, 200)
	policy := DeliveryPolicy{ID: "test-policy", Enabled: true, AutoPublish: true, RecipeIDs: []string{recipe.ID}, DeviceIDs: []string{"device-a"}, ACL: ACL{UserIDs: []int{2}}}
	f.ok(t, "admin", "PUT", "/admin/delivery/policies/"+policy.ID, policy, 200)
	return f, token
}
func deliveryWorkerRequest(t *testing.T, f *fixture, token, path string, body any, status int) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "/api/edgestack/internal/delivery/"+path, bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("%s status %d want %d body %s", path, w.Code, status, w.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func deliveryCreateClaim(t *testing.T, f *fixture, token string) (DeliveryJob, DeliveryClaim) {
	t.Helper()
	request := f.ok(t, "alice", "POST", "/model-requests", map[string]any{"deviceId": "device-a", "taskId": "task-a", "modelName": "Test model", "message": "prepare requested model"}, 201)
	f.ok(t, "admin", "POST", "/admin/model-requests/"+request["id"].(string)+"/resolve", map[string]any{"recipeId": "test-recipe", "policyId": "test-policy", "source": DeliverySource{URI: "https://models.example/model.rknn", SHA256: "sha256:" + strings.Repeat("b", 64), ModelID: "test-model", Format: "rknn"}}, 202)
	response := deliveryWorkerRequest(t, f, token, "jobs/claim", map[string]any{"workerId": "worker-one", "recipeIds": []string{"test-recipe"}}, 200)
	raw, _ := json.Marshal(response["job"])
	var job DeliveryJob
	_ = json.Unmarshal(raw, &job)
	claim := DeliveryClaim{WorkerID: "worker-one", ClaimToken: response["claimToken"].(string), ClaimEpoch: job.ClaimEpoch, ControlEpoch: job.ControlEpoch}
	return job, claim
}
func TestDeliveryRequestACLAndWorkerLease(t *testing.T) {
	f, token := deliveryFixture(t)
	job, claim := deliveryCreateClaim(t, f, token)
	f.ok(t, "bob", "GET", "/model-requests/"+job.RequestID, nil, 404)
	list := f.ok(t, "bob", "GET", "/delivery/jobs", nil, 200)
	if len(list["items"].([]any)) != 0 {
		t.Fatal("cross-user jobs leaked")
	}
	public := f.ok(t, "alice", "GET", "/delivery/jobs/"+job.ID, nil, 200)
	raw, _ := json.Marshal(public)
	if strings.Contains(string(raw), claim.ClaimToken) || strings.Contains(string(raw), "secretHash") {
		t.Fatal("worker proof leaked")
	}
	f.ok(t, "bob", "POST", "/model-requests/"+job.RequestID+"/messages", map[string]string{"message": "unauthorized"}, 404)
	deliveryWorkerRequest(t, f, token, "jobs/"+job.ID+"/heartbeat", claim, 200)
	f.now = f.now.Add(121 * time.Second)
	deliveryWorkerRequest(t, f, token, "jobs/"+job.ID+"/heartbeat", claim, 409)
	if err := f.s.expireDelivery(); err != nil {
		t.Fatal(err)
	}
	var saved deliveryJobRecord
	_ = f.s.db.View(func(tx *bolt.Tx) error { saved, _ = read[deliveryJobRecord](tx, "delivery-jobs", job.ID); return nil })
	if saved.Status != "needs_review" {
		t.Fatal(saved.Status)
	}
	retried := f.ok(t, "admin", "POST", "/admin/model-requests/"+job.RequestID+"/retry", map[string]any{}, 202)
	if retried["id"] != job.ID {
		t.Fatal("unchanged input lost durable stage checkpoints")
	}
	deliveryWorkerRequest(t, f, token, "jobs/"+job.ID+"/heartbeat", claim, 409)

}
func TestDeliveryValidationAndImmutableResult(t *testing.T) {
	f, token := deliveryFixture(t)
	job, claim := deliveryCreateClaim(t, f, token)
	oci, imageDigest := deliveryTestOCI(t)
	model := ModelPackage{ID: "delivery-" + job.ID, Name: "Test model", Version: "1", Kind: "voice", Slot: "llm", TaskKinds: []string{"voice"}, Architectures: []string{"amd64"}, RuntimeBackend: "rknn", TemplateIDs: []string{"test-template"}, Immutable: true, Image: "example/model@" + imageDigest, ModelID: job.Source.ModelID, ModelHash: job.Source.SHA256, Environment: map[string]string{}}
	start := struct {
		DeliveryClaim
		Package ModelPackage `json:"package"`
	}{claim, model}
	response := deliveryWorkerRequest(t, f, token, "jobs/"+job.ID+"/validation/start", start, 200)
	if response["operationId"] == "" || f.dispatcher.applied != 1 {
		t.Fatal(response)
	}
	report := DeliveryReport{ID: "report-one", JobID: job.ID, RecipeDigest: job.RecipeDigest, ReleaseDigest: "sha256:" + strings.Repeat("e", 64), Image: model.Image, ModelHash: model.ModelHash, RuntimeProfileDigest: job.Recipe.RuntimeProfileDigest, DeviceID: "device-a", TaskID: "task-a", Status: "passed", NativeVerified: true, ContainerVerified: true, LatencyMs: 12, MemoryBytes: 1024, ErrorRate: 0, Quality: 1, ObservedAt: f.now}
	complete := struct {
		DeliveryClaim
		Report DeliveryReport `json:"report"`
	}{claim, report}
	deliveryWorkerRequest(t, f, token, "jobs/"+job.ID+"/validation/complete", complete, 503)
	f.s.opts.VerifyModelRuntime = func(context.Context, Task, ModelPackage) error { return nil }
	deliveryWorkerRequest(t, f, token, "jobs/"+job.ID+"/validation/complete", complete, 503)
	f.s.opts.DeliveryObserveHealth = func(context.Context, Task, ModelPackage) error { return fail(503, "unhealthy") }
	deliveryWorkerRequest(t, f, token, "jobs/"+job.ID+"/validation/complete", complete, 503)
	f.s.opts.DeliveryObserveHealth = func(context.Context, Task, ModelPackage) error { return nil }
	response = deliveryWorkerRequest(t, f, token, "jobs/"+job.ID+"/validation/complete", complete, 200)
	raw, _ := json.Marshal(response["report"])
	_ = json.Unmarshal(raw, &report)
	artifact := DeliveryArtifact{ID: "artifact-one", JobID: job.ID, ModelID: model.ModelID, ModelHash: model.ModelHash, Image: model.Image, Platform: "linux/amd64", RecipeDigest: job.RecipeDigest, RuntimeProfileDigest: job.Recipe.RuntimeProfileDigest, ReleaseDigest: report.ReleaseDigest, ReportDigest: report.Digest, RegistryVerified: true, OfflineBundle: DeliveryBlob{URI: "file:///shared/bundle.tar", SHA256: "sha256:" + strings.Repeat("f", 64)}, Files: []DeliveryFile{{Path: "model.rknn", SHA256: model.ModelHash, SizeBytes: 5}}}
	deliverySignedBundle(t, f.s, &artifact, report, oci)
	result := struct {
		DeliveryClaim
		ResultID string           `json:"resultId"`
		Status   string           `json:"status"`
		Artifact DeliveryArtifact `json:"artifact"`
		Report   DeliveryReport   `json:"report"`
	}{claim, "result-one", "artifact_ready", artifact, report}
	response = deliveryWorkerRequest(t, f, token, "jobs/"+job.ID+"/result", result, 200)
	if response["status"] != "published" {
		t.Fatal(response)
	}
	deliveryWorkerRequest(t, f, token, "jobs/"+job.ID+"/result", result, 200)
	result.ResultID = "changed-result"
	deliveryWorkerRequest(t, f, token, "jobs/"+job.ID+"/result", result, 409)
	var published ModelPackage
	_ = f.s.db.View(func(tx *bolt.Tx) error { published, _ = read[ModelPackage](tx, "models", model.ID); return nil })
	if !published.Published || published.ArtifactID != artifact.ID {
		t.Fatal(published)
	}
	f.ok(t, "bob", "GET", "/delivery/artifacts/"+artifact.ID, nil, 404)
	f.ok(t, "team", "GET", "/delivery/artifacts/"+artifact.ID, nil, 404)
	f.ok(t, "team", "GET", "/delivery/artifacts/"+artifact.ID+"/export", nil, 404)
	f.ok(t, "team", "POST", "/tasks/task-a/model-change-preview", map[string]any{"packageId": model.ID, "slot": "llm", "revision": 2}, 409)
	f.ok(t, "team", "POST", "/deployment-plans", map[string]any{"packageId": model.ID, "deviceId": "device-a", "taskId": "task-a"}, 409)
	deniedCatalog := f.ok(t, "team", "GET", "/delivery/catalog", nil, 200)
	if len(deniedCatalog["items"].([]any)) != 0 {
		t.Fatal("policy ACL bypassed catalog")
	}
	var policy DeliveryPolicy
	_ = f.s.db.View(func(tx *bolt.Tx) error {
		policy, _ = read[DeliveryPolicy](tx, "delivery-policies", job.PolicyID)
		return nil
	})
	policy.ACL.TeamIDs = []int{9}
	f.ok(t, "admin", "PUT", "/admin/delivery/policies/"+policy.ID, policy, 200)
	f.ok(t, "team", "GET", "/delivery/artifacts/"+artifact.ID, nil, 200)
	catalog := f.ok(t, "team", "GET", "/delivery/catalog", nil, 200)
	if len(catalog["items"].([]any)) == 0 {
		t.Fatal("published-scope users cannot reuse catalog")
	}
	downloaded := f.request(t, "team", "GET", "/delivery/artifacts/"+artifact.ID+"/export", nil)
	if downloaded.Code != 200 || downloaded.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatal(downloaded.Code, downloaded.Body.String())
	}
	path, _ := url.Parse(artifact.OfflineBundle.URI)
	if err := os.WriteFile(path.Path, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	f.ok(t, "alice", "GET", "/delivery/artifacts/"+artifact.ID+"/export", nil, 409)

}
func TestDeliveryCurrentPolicyRevocationAndRecoveryEpoch(t *testing.T) {
	f, token := deliveryFixture(t)
	job, claim := deliveryCreateClaim(t, f, token)
	policy := f.ok(t, "admin", "GET", "/admin/delivery/policies", nil, 200)["items"].([]any)[0]
	raw, _ := json.Marshal(policy)
	var parsed DeliveryPolicy
	_ = json.Unmarshal(raw, &parsed)
	parsed.Enabled = false
	f.ok(t, "admin", "PUT", "/admin/delivery/policies/"+parsed.ID, parsed, 200)
	deliveryWorkerRequest(t, f, token, "jobs/"+job.ID+"/heartbeat", claim, 409)
	path := f.s.db.Path()
	opts := f.s.opts
	old := f.s.deliveryEpoch
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	service, err := Open(path, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	f.s = service
	f.handler = service.Handler()
	if service.deliveryEpoch == old {
		t.Fatal("control epoch reused")
	}
	deliveryWorkerRequest(t, f, token, "jobs/"+job.ID+"/heartbeat", claim, 409)
}

func deliveryTestTar(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for name, body := range files {
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
func deliveryTestOCI(t *testing.T) ([]byte, string) {
	t.Helper()
	files := map[string][]byte{}
	blob := func(value []byte, media string) deliveryDescriptor {
		sum := sha256.Sum256(value)
		hash := hex.EncodeToString(sum[:])
		files["blobs/sha256/"+hash] = value
		return deliveryDescriptor{MediaType: media, Digest: "sha256:" + hash, Size: int64(len(value))}
	}
	config := blob([]byte(`{"os":"linux","architecture":"amd64"}`), "application/vnd.oci.image.config.v1+json")
	layer := blob([]byte("immutable model layer"), "application/vnd.oci.image.layer.v1.tar")
	manifest, _ := json.Marshal(map[string]any{"schemaVersion": 2, "config": config, "layers": []deliveryDescriptor{layer}})
	descriptor := blob(manifest, "application/vnd.oci.image.manifest.v1+json")
	files["index.json"], _ = json.Marshal(map[string]any{"schemaVersion": 2, "manifests": []deliveryDescriptor{descriptor}})
	files["oci-layout"] = []byte(`{"imageLayoutVersion":"1.0.0"}`)
	return deliveryTestTar(t, files), descriptor.Digest
}
func deliverySignedBundle(t *testing.T, s *Service, artifact *DeliveryArtifact, report DeliveryReport, oci []byte) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	s.opts.DeliveryArtifactRoot = root
	s.opts.DeliveryTrustedPublicKeyFile = filepath.Join(root, "public.pem")
	der, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(s.opts.DeliveryTrustedPublicKeyFile, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	imageSHA := sha256.Sum256(oci)
	manifest, _ := json.Marshal(map[string]any{"schemaVersion": 1, "jobId": artifact.JobID, "image": artifact.Image, "imageDigest": strings.Split(artifact.Image, "@")[1], "imageArchiveSha256": "sha256:" + hex.EncodeToString(imageSHA[:]), "modelHash": artifact.ModelHash, "modelId": artifact.ModelID, "recipeDigest": artifact.RecipeDigest, "runtimeProfileDigest": artifact.RuntimeProfileDigest, "releaseDigest": artifact.ReleaseDigest, "report": report})
	body := deliveryTestTar(t, map[string][]byte{"manifest.json": manifest, "manifest.ed25519": ed25519.Sign(private, manifest), "image.oci.tar": oci})
	path := filepath.Join(root, "artifact.tar")
	if err = os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	artifact.OfflineBundle = DeliveryBlob{URI: (&url.URL{Scheme: "file", Path: path}).String(), SHA256: "sha256:" + hex.EncodeToString(sum[:])}
}

func deliveryRolloutModels(t *testing.T, f *fixture) {
	t.Helper()
	for _, id := range []string{"old", "new"} {
		model := ModelPackage{ID: id, Name: id, Version: "1", Kind: "voice", Slot: "llm", TaskKinds: []string{"voice"}, Architectures: []string{"amd64"}, RuntimeBackend: "rknn", TemplateIDs: []string{"test-template"}, Immutable: true, Published: true, Image: "example/" + id + "@sha256:" + strings.Repeat("a", 64), ModelID: id, ModelHash: "sha256:" + strings.Repeat("b", 64)}
		f.ok(t, "admin", "POST", "/admin/models", model, 200)
	}
}
func completeDeliveryOperation(t *testing.T, f *fixture, operationID string) {
	t.Helper()
	if err := f.s.db.Update(func(tx *bolt.Tx) error {
		op, e := read[Operation](tx, "operations", operationID)
		if e != nil {
			return e
		}
		op.Status = "succeeded"
		return put(tx, "operations", op.ID, op)
	}); err != nil {
		t.Fatal(err)
	}
}
func TestDeliveryRolloutHealthWindowAndRollback(t *testing.T) {
	f, _ := deliveryFixture(t)
	deliveryRolloutModels(t, f)
	f.s.opts.DeliveryObserveHealth = func(context.Context, Task, ModelPackage) error { return nil }
	plan := f.ok(t, "alice", "POST", "/deployment-plans", map[string]any{"deviceId": "device-a", "taskId": "task-a", "packageId": "new"}, 200)
	result := f.ok(t, "alice", "POST", "/deployment-plans/"+plan["id"].(string)+"/confirm", map[string]any{"confirm": true}, 202)
	rolloutID := result["id"].(string)
	operationID := result["operationId"].(string)
	f.ok(t, "alice", "POST", "/deployment-plans/"+plan["id"].(string)+"/confirm", map[string]any{"confirm": true}, 202)
	if f.dispatcher.applied != 1 {
		t.Fatal("duplicate confirm redeployed")
	}
	completeDeliveryOperation(t, f, operationID)
	for _, elapsed := range []time.Duration{0, 10 * time.Second, 10 * time.Second} {
		f.now = f.now.Add(elapsed)
		if err := f.s.advanceDeliveryRollouts(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	result = f.ok(t, "alice", "GET", "/rollouts/"+rolloutID, nil, 200)
	if result["status"] != "running" {
		t.Fatal("health gate finished before120s", result)
	}
	f.now = f.now.Add(100 * time.Second)
	if err := f.s.advanceDeliveryRollouts(context.Background()); err != nil {
		t.Fatal(err)
	}
	result = f.ok(t, "alice", "GET", "/rollouts/"+rolloutID, nil, 200)
	if result["status"] != "succeeded" {
		t.Fatal(result)
	}
	// A later failed canary restores the exact prior package through a separate
	// observed operation; it does not report successful restoration from Apply.
	plan = f.ok(t, "alice", "POST", "/deployment-plans", map[string]any{"deviceId": "device-a", "taskId": "task-a", "packageId": "old"}, 200)
	result = f.ok(t, "alice", "POST", "/deployment-plans/"+plan["id"].(string)+"/confirm", map[string]any{"confirm": true}, 202)
	rolloutID = result["id"].(string)
	completeDeliveryOperation(t, f, result["operationId"].(string))
	f.s.opts.DeliveryObserveHealth = func(context.Context, Task, ModelPackage) error { return fail(503, "probe_failed") }
	if err := f.s.advanceDeliveryRollouts(context.Background()); err != nil {
		t.Fatal(err)
	}
	result = f.ok(t, "alice", "GET", "/rollouts/"+rolloutID, nil, 200)
	if result["status"] != "rolling_back" {
		t.Fatal(result)
	}
	if err := f.s.advanceDeliveryRollouts(context.Background()); err != nil {
		t.Fatal(err)
	}
	result = f.ok(t, "alice", "GET", "/rollouts/"+rolloutID, nil, 200)
	if result["status"] != "rolling_back" {
		t.Fatal(result)
	}
	target := result["targets"].([]any)[0].(map[string]any)
	if target["status"] != "rollback_observing" {
		t.Fatal(target)
	}
	if f.dispatcher.applied != 3 {
		t.Fatal("rollback not actually dispatched", f.dispatcher.applied)
	}
	completeDeliveryOperation(t, f, target["operationId"].(string))
	f.s.opts.DeliveryObserveHealth = func(context.Context, Task, ModelPackage) error { return nil }
	for i := 0; i < 4; i++ {
		f.now = f.now.Add(40 * time.Second)
		if err := f.s.advanceDeliveryRollouts(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	result = f.ok(t, "alice", "GET", "/rollouts/"+rolloutID, nil, 200)
	if result["status"] != "rolled_back" {
		t.Fatal(result)
	}
}

func TestDeliveryRollbackPreservesUnrelatedModelChange(t *testing.T) {
	f, _ := deliveryFixture(t)
	deliveryRolloutModels(t, f)
	f.s.opts.DeliveryObserveHealth = func(context.Context, Task, ModelPackage) error { return fail(503, "probe_failed") }
	plan := f.ok(t, "alice", "POST", "/deployment-plans", map[string]any{"deviceId": "device-a", "taskId": "task-a", "packageId": "new"}, 200)
	result := f.ok(t, "alice", "POST", "/deployment-plans/"+plan["id"].(string)+"/confirm", map[string]any{"confirm": true}, 202)
	completeDeliveryOperation(t, f, result["operationId"].(string))
	if err := f.s.advanceDeliveryRollouts(context.Background()); err != nil {
		t.Fatal(err)
	}
	// A separate authorized deployment can replace the candidate before the
	// rollback tick. Recovery must not overwrite that unrelated model.
	if err := f.s.db.Update(func(tx *bolt.Tx) error {
		task, err := read[Task](tx, "tasks", "task-a")
		if err != nil {
			return err
		}
		task.Models[0].PackageID = "unrelated-model"
		return put(tx, "tasks", task.ID, task)
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.advanceDeliveryRollouts(context.Background()); err != nil {
		t.Fatal(err)
	}
	result = f.ok(t, "alice", "GET", "/rollouts/"+result["id"].(string), nil, 200)
	if result["status"] != "needs_review" || result["errorCode"] != "delivery_rollback_unconfirmed" || f.dispatcher.applied != 1 {
		t.Fatal("rollback overwrote an unrelated deployment", result, f.dispatcher.applied)
	}
}

type deliveryTestProvisioner struct {
	rollbacks  int
	onRollback func() error
}

func (*deliveryTestProvisioner) Preview(context.Context, DeliveryProvisionSpec) (DeliveryProvisionPreview, error) {
	return DeliveryProvisionPreview{TargetVersion: "1"}, nil
}
func (*deliveryTestProvisioner) Apply(_ context.Context, spec DeliveryProvisionSpec, _ DeliveryProvisionPreview, _ string) (Task, error) {
	return Task{ID: spec.TaskID, DeviceID: spec.Device.ID, Name: spec.Name, Kind: spec.Recipe.TaskKind,
		RuntimeBackend: spec.Recipe.RuntimeBackend, TemplateID: spec.Recipe.TemplateID,
		Target: Target{EndpointID: spec.Device.EndpointID, StackID: 3, ServiceID: "model"}, Capabilities: []string{"replace-model"}}, nil
}
func (p *deliveryTestProvisioner) Rollback(context.Context, DeliveryProvisionSpec, string) error {
	p.rollbacks++
	if p.onRollback != nil {
		return p.onRollback()
	}
	return nil
}

func TestDeliveryNewTaskRollbackPreservesChangedTask(t *testing.T) {
	for _, change := range []string{"revision", "package", "during_archive"} {
		t.Run(change, func(t *testing.T) {
			f, _ := deliveryFixture(t)
			deliveryRolloutModels(t, f)
			if err := f.s.db.Update(func(tx *bolt.Tx) error {
				return put(tx, "model-policies", "new", ModelPolicy{MetadataVersion: 1, ValidationStatus: "passed", ValidatedScopes: []PublicationScope{{TaskKind: "voice", Architecture: "amd64", RuntimeBackend: "rknn", TemplateID: "test-template"}}})
			}); err != nil {
				t.Fatal(err)
			}
			provider := &deliveryTestProvisioner{}
			f.s.opts.DeliveryProvisioner = provider
			f.s.opts.DeliveryObserveHealth = func(context.Context, Task, ModelPackage) error { return fail(503, "probe_failed") }
			plan := f.ok(t, "alice", "POST", "/deployment-plans", map[string]any{"deviceId": "device-a", "packageId": "new", "recipeId": "test-recipe"}, 200)
			result := f.ok(t, "alice", "POST", "/deployment-plans/"+plan["id"].(string)+"/confirm", map[string]any{"confirm": true}, 202)
			taskID := result["taskId"].(string)
			completeDeliveryOperation(t, f, result["operationId"].(string))
			if err := f.s.advanceDeliveryRollouts(context.Background()); err != nil {
				t.Fatal(err)
			}
			mutate := func() error {
				return f.s.db.Update(func(tx *bolt.Tx) error {
					task, err := read[Task](tx, "tasks", taskID)
					if err != nil {
						return err
					}
					if change == "package" {
						task.Models[0].PackageID = "third-model"
					} else {
						task.Revision++
					}
					return put(tx, "tasks", task.ID, task)
				})
			}
			if change == "during_archive" {
				provider.onRollback = mutate
			} else if err := mutate(); err != nil {
				t.Fatal(err)
			}
			if err := f.s.advanceDeliveryRollouts(context.Background()); err != nil {
				t.Fatal(err)
			}
			result = f.ok(t, "alice", "GET", "/rollouts/"+result["id"].(string), nil, 200)
			wantRollbacks := 0
			if change == "during_archive" {
				wantRollbacks = 1
			}
			var task Task
			_ = f.s.db.View(func(tx *bolt.Tx) error { task, _ = read[Task](tx, "tasks", taskID); return nil })
			if result["status"] != "needs_review" || provider.rollbacks != wantRollbacks || task.Archived {
				t.Fatal("changed new task was stopped or archived", result, provider.rollbacks, task)
			}
		})
	}
}
