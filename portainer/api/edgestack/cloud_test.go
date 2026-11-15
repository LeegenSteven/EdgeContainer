package edgestack

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

const fakeHostCloudKey = "fake-cloud-host-secret-alpha"

func modelDirectoryResponse() *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: ioNop([]byte(`{"data":[{"id":"deepseek-flash"},{"id":"model-b"}]}`))}
}

func cloudChatResponse(reference, text string) *http.Response {
	content, _ := json.Marshal(map[string]any{"title": "小结", "summary": text, "topics": []Topic{{Text: text, EventRefs: []string{reference}}}, "answer": "", "uncertainties": []string{}})
	body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(content)}}}})
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: ioNop(body)}
}

func setupCloud(t *testing.T, transport func(*http.Request) (*http.Response, error)) *fixture {
	t.Helper()
	f := setup(t)
	file := filepath.Join(t.TempDir(), "host.key")
	if err := os.WriteFile(file, []byte(fakeHostCloudKey), 0600); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripFunc(transport)}
	provider := &DeepSeekProvider{SecretFile: file, Model: "deepseek-flash", Client: client}
	f.s.opts.Provider, f.s.cloud.host, f.s.cloud.client = provider, provider, client
	return f
}

func cloudBody(version uint64, key string) cloudUpdate {
	return cloudUpdate{MetadataVersion: version, Provider: "deepseek", Model: "model-b", APIKey: &key}
}

func TestCloudSettingsAdminOnlyValidationUsesOnlyOfficialModels(t *testing.T) {
	requests := 0
	f := setupCloud(t, func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Method != "GET" || r.URL.String() != DeepSeekModelsURL || r.Body != nil || r.ContentLength != 0 {
			t.Fatal("validation sent a payload or nonofficial request")
		}
		deadline, exists := r.Context().Deadline()
		if !exists || time.Until(deadline) > 10*time.Second || r.Header.Get("Authorization") != "Bearer candidate-test-key" {
			t.Fatal("validation deadline or isolated candidate authentication missing")
		}
		return modelDirectoryResponse(), nil
	})
	for _, path := range []string{"/admin/settings/cloud", "/admin/settings/cloud/test", "/admin/settings/cloud/restore-hostfile"} {
		method := "POST"
		if path == "/admin/settings/cloud" {
			method = "GET"
		}
		f.ok(t, "alice", method, path, nil, 403)
		f.ok(t, "", method, path, nil, 401)
	}
	initial := f.ok(t, "admin", "GET", "/admin/settings/cloud", nil, 200)
	if initial["source"] != "hostfile" || initial["hasKey"] != true || initial["metadataVersion"] != float64(1) {
		t.Fatal(initial)
	}
	out := f.ok(t, "admin", "POST", "/admin/settings/cloud/test", cloudBody(1, "candidate-test-key"), 200)
	encoded, _ := json.Marshal(out)
	if strings.Contains(string(encoded), "candidate-test-key") || strings.Contains(string(encoded), fakeHostCloudKey) || requests != 1 {
		t.Fatal("secret echoed or unexpected provider requests")
	}
	record, _ := f.s.cloud.record()
	if record.Source != "hostfile" || record.MetadataVersion != 1 {
		t.Fatal("test endpoint mutated active settings")
	}
	f.ok(t, "admin", "PATCH", "/admin/settings/cloud", cloudBody(0, "ignored"), 409)
	f.ok(t, "admin", "PATCH", "/admin/settings/cloud", cloudBody(1, " "), 400)
	f.ok(t, "admin", "POST", "/admin/settings/cloud/test", map[string]any{"metadataVersion": 1, "provider": "deepseek", "model": "model-b", "endpoint": "https://example.test"}, 400)
	if requests != 1 {
		t.Fatal("invalid input reached provider")
	}
}

func TestCloudValidationRejectsRedirectsAndProviderCredentialEcho(t *testing.T) {
	key, calls := "fake-candidate-never-echo", 0
	f := setupCloud(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != DeepSeekModelsURL {
			t.Fatal("credential followed a provider redirect")
		}
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://other.example/models"}}, Body: ioNop(nil)}, nil
	})
	f.s.cloud.client.CheckRedirect = func(*http.Request, []*http.Request) error { return nil }
	f.ok(t, "admin", "POST", "/admin/settings/cloud/test", cloudBody(1, key), 502)
	if calls != 1 {
		t.Fatal("redirect triggered another request")
	}
	f.s.cloud.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: ioNop([]byte(`{"data":[{"id":"model-b"},{"id":"` + key + `"}]}`))}, nil
	})
	result := f.ok(t, "admin", "POST", "/admin/settings/cloud/test", cloudBody(1, key), 200)
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), key) {
		t.Fatal("provider model listing echoed a credential")
	}
}

func TestCloudManagedSavePrivateFilesRestartAndMissingFileFailClosed(t *testing.T) {
	f := setupCloud(t, func(r *http.Request) (*http.Response, error) { return modelDirectoryResponse(), nil })
	key := "new-managed-key-for-test"
	response := f.ok(t, "admin", "PATCH", "/admin/settings/cloud", cloudBody(1, key), 200)
	encoded, _ := json.Marshal(response)
	if strings.Contains(string(encoded), key) || strings.Contains(string(encoded), "secretRef") || response["source"] != "managed" || response["metadataVersion"] != float64(2) {
		t.Fatal("unsafe settings projection")
	}
	record, _ := f.s.cloud.record()
	file := filepath.Join(f.s.cloud.dir, record.SecretRef)
	for path, permission := range map[string]os.FileMode{f.s.cloud.dir: 0700, file: 0600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != permission {
			t.Fatalf("managed permissions wrong for %s", filepath.Base(path))
		}
	}
	if err := f.s.db.View(func(tx *bolt.Tx) error {
		if strings.Contains(string(tx.Bucket([]byte("cloud-settings")).Get([]byte("active"))), key) {
			t.Fatal("key persisted in metadata")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// A fresh service cannot reset web settings back to its host defaults.
	dbPath, options := f.s.db.Path(), f.s.opts
	f.s.Close()
	service, err := Open(dbPath, options)
	if err != nil {
		t.Fatal(err)
	}
	f.s, f.handler = service, service.Handler()
	t.Cleanup(func() { service.Close() })
	after, _ := service.cloud.record()
	if after.MetadataVersion != 2 || after.SecretRef != record.SecretRef || after.Model != "model-b" {
		t.Fatal("restart overrode managed state")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	view := f.ok(t, "admin", "GET", "/admin/settings/cloud", nil, 200)
	if view["hasKey"] != false || view["source"] != "managed" {
		t.Fatal("missing managed key silently fell back")
	}
	if _, _, err := service.freezeSummaryProvider(); providerCode(err) != "provider_not_configured" {
		t.Fatal("missing managed key did not fail closed")
	}
	f.ok(t, "admin", "POST", "/admin/settings/cloud/restore-hostfile", map[string]any{"metadataVersion": 1}, 409)
	view = f.ok(t, "admin", "POST", "/admin/settings/cloud/restore-hostfile", map[string]any{"metadataVersion": 2}, 200)
	if view["source"] != "hostfile" || view["model"] != "deepseek-flash" || view["metadataVersion"] != float64(3) {
		t.Fatal("explicit hostfile restoration failed")
	}
}

func TestCloudValidationAndStorageFailuresPreserveActiveReference(t *testing.T) {
	f := setupCloud(t, func(*http.Request) (*http.Response, error) { return modelDirectoryResponse(), nil })
	f.ok(t, "admin", "PATCH", "/admin/settings/cloud", cloudBody(1, "active-managed-key"), 200)
	before, _ := f.s.cloud.record()
	for _, failure := range []string{"write", "commit", "authentication", "timeout"} {
		t.Run(failure, func(t *testing.T) {
			write, commit, transport := f.s.cloud.writeFile, f.s.cloud.commit, f.s.cloud.client.Transport
			defer func() { f.s.cloud.writeFile, f.s.cloud.commit, f.s.cloud.client.Transport = write, commit, transport }()
			status := 500
			switch failure {
			case "write":
				f.s.cloud.writeFile = func(string, string) (string, error) { return "", errors.New("fake write failure") }
			case "commit":
				f.s.cloud.commit = func(func(*bolt.Tx) error) error { return errors.New("fake DB transaction failure") }
			case "authentication":
				status = 502
				f.s.cloud.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 401, Body: ioNop([]byte("must-not-return-provider-body"))}, nil
				})
			case "timeout":
				status = 502
				f.s.cloud.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded })
			}
			response := f.request(t, "admin", "PATCH", "/admin/settings/cloud", cloudBody(2, "failed-candidate-key"))
			if response.Code != status || strings.Contains(response.Body.String(), "failed-candidate-key") || strings.Contains(response.Body.String(), "must-not-return-provider-body") {
				t.Fatal("failure leaked credentials or wrong response")
			}
			after, _ := f.s.cloud.record()
			if after.SecretRef != before.SecretRef || after.MetadataVersion != before.MetadataVersion {
				t.Fatal("failed save switched active reference")
			}
			if key, err := f.s.cloud.key(after); err != nil || key != "active-managed-key" {
				t.Fatal("failed save lost active secret")
			}
		})
	}
	if err := os.Remove(f.s.cloud.host.SecretFile); err != nil {
		t.Fatal(err)
	}
	f.ok(t, "admin", "POST", "/admin/settings/cloud/restore-hostfile", map[string]any{"metadataVersion": 2}, 502)
	after, _ := f.s.cloud.record()
	if after.SecretRef != before.SecretRef || after.MetadataVersion != before.MetadataVersion {
		t.Fatal("failed host restoration switched a valid managed source")
	}
}

func TestCloudConcurrentSaveHasSingleCASWinner(t *testing.T) {
	ready, release := make(chan struct{}, 2), make(chan struct{})
	f := setupCloud(t, func(*http.Request) (*http.Response, error) {
		ready <- struct{}{}
		<-release
		return modelDirectoryResponse(), nil
	})
	results := make(chan int, 2)
	for _, key := range []string{"candidate-alpha", "candidate-beta"} {
		go func(key string) {
			results <- f.request(t, "admin", "PATCH", "/admin/settings/cloud", cloudBody(1, key)).Code
		}(key)
	}
	<-ready
	<-ready
	close(release)
	codes := []int{<-results, <-results}
	if !(codes[0] == 200 && codes[1] == 409 || codes[0] == 409 && codes[1] == 200) {
		t.Fatal(codes)
	}
	record, _ := f.s.cloud.record()
	entries, err := os.ReadDir(f.s.cloud.dir)
	if err != nil || len(entries) != 1 || record.MetadataVersion != 2 {
		t.Fatal("CAS loser retained an active/orphan candidate")
	}
}

func TestDeepSeekSnapshotSurvivesHostFileAndModelRotation(t *testing.T) {
	file := filepath.Join(t.TempDir(), "host.key")
	os.WriteFile(file, []byte("old-snapshot-key"), 0600)
	provider := &DeepSeekProvider{SecretFile: file, Model: "old-model"}
	frozen, err := provider.freeze()
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(file, []byte("new-snapshot-key"), 0600)
	provider.Model = "new-model"
	key, err := frozen.secretKey()
	if err != nil || key != "old-snapshot-key" || frozen.Model != "old-model" {
		t.Fatal("running summary did not own immutable credentials/model")
	}
}

func TestCloudInvalidHostKeyFailsBeforeAnyProviderRequest(t *testing.T) {
	file := filepath.Join(t.TempDir(), "invalid-host.key")
	for _, value := range []string{"", "\n", "fake\x00key", "fake\nkey"} {
		if err := os.WriteFile(file, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		provider := &DeepSeekProvider{SecretFile: file, Model: "deepseek-flash", Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Error("invalid host key reached provider transport")
			return nil, errors.New("must not call")
		})}}
		_, err := provider.Summarize(context.Background(), SummaryRequest{}, []Evidence{{Ref: "evt:test", Text: "test"}})
		if providerCode(err) != "provider_not_configured" {
			t.Fatal("invalid host file was accepted")
		}
	}
}

func TestCloudRotationFreezesSummaryKeyModelAndRedaction(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	keys, models := []string{}, []string{}
	f := setupCloud(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.String() == DeepSeekModelsURL {
			return modelDirectoryResponse(), nil
		}
		if r.URL.String() != DeepSeekURL {
			t.Fatal("summary endpoint changed")
		}
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Model    string `json:"model"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.Unmarshal(body, &payload)
		var input struct {
			Evidence []struct {
				Ref string `json:"ref"`
			} `json:"evidence"`
		}
		json.Unmarshal([]byte(payload.Messages[1].Content), &input)
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if strings.Contains(string(body), fakeHostCloudKey) {
			t.Error("snapshot secret reached summary evidence")
		}
		mu.Lock()
		keys, models = append(keys, key), append(models, payload.Model)
		first := len(keys) == 1
		mu.Unlock()
		if first {
			close(started)
			<-release
		}
		return cloudChatResponse(input.Evidence[0].Ref, "summary "+key), nil
	})
	event := f.event("cloud-rotation")
	event.Text = "record " + fakeHostCloudKey
	f.ingest(t, event)
	request := SummaryRequest{DeviceID: "device-a", From: f.now.Add(-time.Hour * 24), To: f.now}
	finished := make(chan string, 1)
	go func() { finished <- f.request(t, "alice", "POST", "/summaries", request).Body.String() }()
	<-started
	f.ok(t, "admin", "PATCH", "/admin/settings/cloud", cloudBody(1, "rotated-cloud-key-beta"), 200)
	close(release)
	first := <-finished
	if strings.Contains(first, fakeHostCloudKey) || !strings.Contains(first, credentialMarker) {
		t.Fatal("old snapshot leaked or used a different redaction key")
	}
	f.ok(t, "alice", "POST", "/summaries", request, 201)
	mu.Lock()
	defer mu.Unlock()
	if len(keys) != 2 || keys[0] != fakeHostCloudKey || keys[1] != "rotated-cloud-key-beta" || models[0] != "deepseek-flash" || models[1] != "model-b" {
		t.Fatal("summary retried across a key boundary or failed to rotate its snapshot")
	}
}

func TestCloudOldRequestAuthenticationFailureNeverRetriesWithRotatedKey(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	keys := []string{}
	f := setupCloud(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.String() == DeepSeekModelsURL {
			return modelDirectoryResponse(), nil
		}
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		mu.Lock()
		keys = append(keys, key)
		first := len(keys) == 1
		mu.Unlock()
		if first {
			close(started)
			<-release
			return &http.Response{StatusCode: 401, Body: ioNop([]byte(fakeHostCloudKey))}, nil
		}
		return cloudChatResponse("evt:old-request-failure", "新请求完成。"), nil
	})
	f.ingest(t, f.event("old-request-failure"))
	request := SummaryRequest{DeviceID: "device-a", From: f.now.Add(-24 * time.Hour), To: f.now}
	finished := make(chan map[string]any, 1)
	go func() { finished <- f.ok(t, "alice", "POST", "/summaries", request, 201) }()
	<-started
	f.ok(t, "admin", "PATCH", "/admin/settings/cloud", cloudBody(1, "new-key-after-old-fails"), 200)
	close(release)
	result := <-finished
	if result["status"] != "failed" || result["errorCode"] != "provider_authentication_failed" {
		t.Fatal("old request did not retain its own failure")
	}
	mu.Lock()
	count := len(keys)
	mu.Unlock()
	if count != 1 {
		t.Fatal("old request retried with a newly configured key")
	}
	f.ok(t, "alice", "POST", "/summaries", request, 201)
	mu.Lock()
	defer mu.Unlock()
	if len(keys) != 2 || keys[0] != fakeHostCloudKey || keys[1] != "new-key-after-old-fails" {
		t.Fatal("next request did not pick up rotated credentials")
	}
}
