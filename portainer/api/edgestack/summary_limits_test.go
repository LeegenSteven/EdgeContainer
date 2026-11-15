package edgestack

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

func TestSummaryRejectsOversizedRangeWithoutCloudOrTruncation(t *testing.T) {
	for _, test := range []struct {
		name        string
		count, size int
		status      int
	}{{"201-events", 201, 10, 400}, {"over-64k", 5, 16384, 400}, {"200-full-events", 200, 100, 201}, {"no-600-character-truncation", 1, 1000, 201}} {
		t.Run(test.name, func(t *testing.T) {
			f := setup(t)
			events := make([]Event, test.count)
			for i := range events {
				events[i] = f.event(fmt.Sprintf("event-%d", i))
				events[i].Sequence = int64(i)
				events[i].Text = strings.Repeat("x", test.size)
			}
			f.ok(t, f.token, "POST", "/ingest/events", map[string]any{"events": events}, 200)
			calls := 0
			f.s.opts.Provider = providerFunc(func(_ context.Context, request SummaryRequest, e []Evidence) (Summary, error) {
				calls++
				if len(e) != test.count || len(e[0].Text) != test.size || request.Coverage.EventCount != test.count {
					t.Error("evidence was truncated or coverage omitted")
				}
				return Summary{Title: "摘要", Summary: "依据记录。", Topics: []Topic{{Text: "记录", EventRefs: []string{e[0].Ref}}}}, nil
			})
			result := f.ok(t, "alice", "POST", "/summaries", SummaryRequest{DeviceID: "device-a", From: f.now.Add(-24 * time.Hour), To: f.now}, test.status)
			if test.status == 400 {
				if result["errorCode"] != "summary_range_too_large" || calls != 0 {
					t.Fatal(result, calls)
				}
			} else if calls != 1 || result["status"] != "completed" {
				t.Fatal(result, calls)
			}
		})
	}
}

func TestSummaryPerUserGuardPreservesGlobalCapacity(t *testing.T) {
	f := setup(t)
	f.ingest(t, f.event("guard-event"))
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	var calls atomic.Int32
	f.s.opts.Provider = providerFunc(func(ctx context.Context, _ SummaryRequest, e []Evidence) (Summary, error) {
		calls.Add(1)
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return Summary{}, ctx.Err()
		}
		return Summary{Title: "摘要", Summary: "记录", Topics: []Topic{{Text: "记录", EventRefs: []string{e[0].Ref}}}}, nil
	})
	request := SummaryRequest{DeviceID: "device-a", From: f.now.Add(-24 * time.Hour), To: f.now}
	results := make(chan *httptest.ResponseRecorder, 2)
	go func() { results <- f.request(t, "alice", "POST", "/summaries", request) }()
	<-started
	f.ok(t, "alice", "POST", "/summaries", request, 429)
	go func() { results <- f.request(t, "team", "POST", "/summaries", request) }()
	<-started
	capacity := f.ok(t, "admin", "POST", "/summaries", request, 429)
	if capacity["errorCode"] != "summary_capacity_reached" || calls.Load() != 2 {
		t.Fatal(capacity)
	}
	close(release)
	for i := 0; i < 2; i++ {
		if result := <-results; result.Code != 201 {
			t.Fatalf("in-flight summary failed: %d %s", result.Code, result.Body.String())
		}
	}
	f.ok(t, "alice", "POST", "/summaries", request, 201)
	f.ok(t, "admin", "POST", "/summaries", request, 201)
	if calls.Load() != 4 {
		t.Fatal("user guard leaked after completion/capacity rejection")
	}
}

type secretProvider struct {
	providerFunc
	secrets []string
}

func (p secretProvider) RedactionSecrets() ([]string, error) { return p.secrets, nil }

func TestSummaryRedactsSnapshotQuestionAndProvenanceBeforeProvider(t *testing.T) {
	f := setup(t)
	serverKey := "server-exact-private-value-0123456789"
	event := f.event("redaction-event")
	event.Text = "hello " + serverKey + " Authorization: Bearer private-bearer-credential sk-abcdefghijk es_ingest_privatevalue password=hunter2 token=standalone-value"
	event.Provenance = json.RawMessage(`{"kind":"integration_test","password":"json-password-value","nested":{"api_key":"json-api-key-value","note":"Bearer nested-bearer-value"}}`)
	f.ingest(t, event)
	f.s.opts.Provider = secretProvider{secrets: []string{serverKey}, providerFunc: func(_ context.Context, request SummaryRequest, e []Evidence) (Summary, error) {
		encoded, _ := json.Marshal(map[string]any{"question": request.Question, "evidence": e})
		for _, secret := range []string{serverKey, "private-bearer-credential", "sk-abcdefghijk", "es_ingest_privatevalue", "hunter2", "standalone-value", "json-password-value", "json-api-key-value", "nested-bearer-value", "question-password"} {
			if strings.Contains(string(encoded), secret) {
				t.Errorf("credential reached provider: %s", secret)
			}
		}
		if !strings.Contains(string(encoded), "integration_test") || request.Coverage.EventCount != 1 {
			t.Error("test provenance or server coverage lost")
		}
		return Summary{Title: "测试摘要", Summary: "集成测试记录。", Topics: []Topic{{Text: "测试", EventRefs: []string{e[0].Ref}}}}, nil
	}}
	result := f.ok(t, "alice", "POST", "/summaries", SummaryRequest{DeviceID: "device-a", From: f.now.Add(-24 * time.Hour), To: f.now, Question: "password=question-password " + serverKey}, 201)
	if result["status"] != "completed" {
		t.Fatal(result)
	}
	snapshot, _ := json.Marshal(result["evidence"])
	if strings.Contains(string(snapshot), serverKey) || !strings.Contains(string(snapshot), credentialMarker) {
		t.Fatal("stored evidence is not the redacted snapshot")
	}
	raw := f.ok(t, "alice", "GET", "/events?deviceId=device-a", nil, 200)
	original, _ := json.Marshal(raw)
	if !strings.Contains(string(original), serverKey) {
		t.Fatal("redaction unexpectedly rewrote original local events")
	}
	f.ok(t, "alice", "POST", "/summaries", map[string]any{"deviceId": "device-a", "from": f.now.Add(-time.Hour), "to": f.now, "coverage": map[string]any{"complete": true}}, 400)
}

func TestDeepSeekCarriesCoverageRedactsExactSecretAndRejectsLengthFinish(t *testing.T) {
	secret := "exact-server-private-secret-0123456789"
	file := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(file, []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	provider := &DeepSeekProvider{SecretFile: file, Model: "deepseek-flash"}
	provider.Client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		messages := body["messages"].([]any)
		system := messages[0].(map[string]any)["content"].(string)
		user := messages[1].(map[string]any)["content"].(string)
		if !strings.Contains(system, "简体中文") || strings.Contains(user, secret) || !strings.Contains(user, `"coverage"`) {
			t.Fatal("language, credential filtering or coverage was omitted")
		}
		content := `{"title":"标题","summary":"摘要","topics":[{"text":"主题","eventRefs":["evt:e"]}],"answer":"","uncertainties":[]}`
		response, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"finish_reason": "length", "message": map[string]any{"content": content}}}})
		return &http.Response{StatusCode: 200, Body: ioNop(response), Header: make(http.Header)}, nil
	})}
	_, err := provider.Summarize(context.Background(), SummaryRequest{Question: secret, Coverage: Coverage{Complete: false}}, []Evidence{{Ref: "evt:" + secret, EventID: secret, Text: secret}})
	if providerCode(err) != "provider_output_truncated" {
		t.Fatalf("parseable length-limited output was accepted: %v", err)
	}
}

func TestCredentialRedactionIsIdempotent(t *testing.T) {
	text := `password=short Authorization: Bearer abc EDGE_KEY=edge-secret-value sk-a token=abc`
	once := redactText(text, nil)
	twice := redactText(once, nil)
	if once != twice || strings.Contains(once, "edge-secret-value") || strings.Contains(once, "sk-a") || strings.Contains(once, "Bearer abc") {
		t.Fatalf("credential filtering was incomplete or unstable: %q %q", once, twice)
	}
}

func TestSummaryQuestionAcceptsTwoThousandChineseCharacters(t *testing.T) {
	f := setup(t)
	f.ingest(t, f.event("chinese-question"))
	calls := 0
	f.s.opts.Provider = providerFunc(func(_ context.Context, request SummaryRequest, e []Evidence) (Summary, error) {
		calls++
		if utf8.RuneCountInString(request.Question) != 2000 || len(request.Question) != 6000 {
			t.Error("Chinese question changed")
		}
		return Summary{Title: "摘要", Summary: "记录", Topics: []Topic{{Text: "记录", EventRefs: []string{e[0].Ref}}}}, nil
	})
	request := SummaryRequest{DeviceID: "device-a", From: f.now.Add(-24 * time.Hour), To: f.now, Question: strings.Repeat("中文", 1000)}
	result := f.ok(t, "alice", "POST", "/summaries", request, 201)
	if result["status"] != "completed" || calls != 1 {
		t.Fatal(result)
	}
	request.Question += "中"
	f.ok(t, "alice", "POST", "/summaries", request, 400)
	if calls != 1 {
		t.Fatal("overlong question reached provider")
	}
}
