package edgestack

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func TestEventsFilterAuthorizedWindowBeforePagination(t *testing.T) {
	f := setup(t)
	events := []Event{}
	for i, kind := range []string{"voice.user_utterance", "voice.assistant_reply", "voice.playback_state", "voice.user_utterance", "voice.assistant_reply"} {
		event := f.event("filter-" + string(rune('a'+i)))
		event.Type = kind
		event.OccurredAt = f.now.Add(time.Duration(i-6) * time.Minute)
		event.SessionID = "session-one"
		if i >= 3 {
			event.SessionID = "session-two"
		}
		events = append(events, event)
	}
	f.ok(t, f.token, "POST", "/ingest/events", map[string]any{"events": events}, 200)
	all := f.ok(t, "alice", "GET", "/events?deviceId=device-a", nil, 200)
	if all["filteredCount"] != float64(5) {
		t.Fatal(all)
	}
	single := f.ok(t, "alice", "GET", "/events?deviceId=device-a&type=voice.user_utterance", nil, 200)
	if len(single["items"].([]any)) != 2 || single["coverage"].(map[string]any)["eventCount"] != float64(5) {
		t.Fatal(single)
	}
	windowed := f.ok(t, "alice", "GET", "/events?deviceId=device-a&type=voice.user_utterance&from="+f.now.Add(-4*time.Minute).Format(time.RFC3339), nil, 200)
	if windowed["filteredCount"] != float64(1) || windowed["coverage"].(map[string]any)["eventCount"] != float64(3) {
		t.Fatal("filter escaped authorized time window", windowed)
	}
	path := "/events?deviceId=device-a&type=voice.user_utterance,voice.assistant_reply&sessionId=session-one&limit=1"
	first := f.ok(t, "alice", "GET", path, nil, 200)
	if first["filteredCount"] != float64(2) || len(first["items"].([]any)) != 1 || first["nextCursor"] != "filter-b" {
		t.Fatal(first)
	}
	second := f.ok(t, "alice", "GET", path+"&cursor=filter-b", nil, 200)
	if second["filteredCount"] != float64(2) || second["items"].([]any)[0].(map[string]any)["eventId"] != "filter-a" || second["nextCursor"] != "" {
		t.Fatal(second)
	}
	f.ok(t, "alice", "GET", path+"&cursor=filter-d", nil, 400)
	session := f.ok(t, "alice", "GET", "/events?deviceId=device-a&sessionId=session-one", nil, 200)
	if session["filteredCount"] != float64(3) {
		t.Fatal(session)
	}
	empty := f.ok(t, "alice", "GET", "/events?deviceId=device-a&type=camera.observation", nil, 200)
	if empty["filteredCount"] != float64(0) || empty["coverage"].(map[string]any)["eventCount"] != float64(5) {
		t.Fatal(empty)
	}
	f.ok(t, "alice", "GET", "/events?deviceId=device-a&type=unsupported", nil, 400)
	f.ok(t, "bob", "GET", "/events?deviceId=device-a&type=unsupported", nil, 404)
}

func TestPreviewAndOperationRetainPreviousPackageIncludingLegacyPreview(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "new-preview", true: "legacy-preview"}[legacy], func(t *testing.T) {
			f := setup(t)
			model := ModelPackage{ID: "new", Name: "New", Version: "2", Slot: "llm", TaskKinds: []string{"voice"}, Architectures: []string{"amd64"}, Published: true, Immutable: true, Image: "new@sha256:" + strings.Repeat("a", 64), ModelID: "qwen3-asr", ModelHash: "sha256:" + strings.Repeat("b", 64)}
			f.ok(t, "admin", "POST", "/admin/models", model, 200)
			preview := f.ok(t, "alice", "POST", "/tasks/task-a/model-change-preview", map[string]any{"packageId": "new", "slot": "llm", "revision": 1}, 200)
			if preview["previousPackageId"] != "old" {
				t.Fatal(preview)
			}
			if legacy {
				if err := f.s.db.Update(func(tx *bolt.Tx) error {
					record, e := read[previewRecord](tx, "previews", preview["id"].(string))
					if e != nil {
						return e
					}
					record.Preview.PreviousPackageID = ""
					return put(tx, "previews", record.Preview.ID, record)
				}); err != nil {
					t.Fatal(err)
				}
			}
			op := f.ok(t, "alice", "POST", "/tasks/task-a/model-changes", map[string]any{"previewId": preview["id"], "confirm": true}, 202)
			if op["previousPackageId"] != "old" {
				t.Fatal(op)
			}
			readback := f.ok(t, "alice", "GET", "/operations/"+op["id"].(string), nil, 200)
			if readback["previousPackageId"] != "old" {
				t.Fatal("previous package was not persisted")
			}
		})
	}
}

type runtimeProjectionProvider struct {
	mountProviderFunc
	observe func(context.Context, Task) RuntimeObservation
}

func (p runtimeProjectionProvider) ObserveRuntime(ctx context.Context, task Task) RuntimeObservation {
	return p.observe(ctx, task)
}

func TestRuntimeProjectionWithoutMountRequestsIsAuthorizedAndSeparateFromState(t *testing.T) {
	f := setup(t)
	var observations atomic.Int32
	if err := f.s.db.Update(func(tx *bolt.Tx) error {
		task, e := read[Task](tx, "tasks", "task-a")
		if e != nil {
			return e
		}
		task.State = "registered"
		task.Models[0].State = "loaded"
		task.Models[0].ObservedPackageID = "old"
		return put(tx, "tasks", task.ID, task)
	}); err != nil {
		t.Fatal(err)
	}
	f.s.opts.MountProvider = runtimeProjectionProvider{mountProviderFunc: func(context.Context, Task, []MountRequest) []MountVerification {
		t.Error("empty mount request queried mounts")
		return nil
	}, observe: func(_ context.Context, task Task) RuntimeObservation {
		observations.Add(1)
		if task.ID != "task-a" {
			t.Error("runtime query escaped authorized task")
		}
		if err := f.s.db.Update(func(tx *bolt.Tx) error {
			source, e := read[Source](tx, "sources", "source-a")
			if e != nil {
				return e
			}
			source.ErrorCode = "runtime_query_outside_tx"
			return put(tx, "sources", source.ID, source)
		}); err != nil {
			t.Error(err)
		}
		return RuntimeObservation{State: "running", ObservedAt: f.now}
	}}
	device := f.ok(t, "alice", "GET", "/devices/device-a", nil, 200)
	task := device["tasks"].([]any)[0].(map[string]any)
	if task["runtimeState"] != "running" || task["runtimeObservedAt"] == nil || task["state"] != "registered" || observations.Load() != 1 {
		t.Fatal(task)
	}
	f.ok(t, "bob", "GET", "/devices/device-a", nil, 404)
	if observations.Load() != 1 {
		t.Fatal("unauthorized runtime query")
	}
	f.s.opts.MountProvider = runtimeProjectionProvider{observe: func(context.Context, Task) RuntimeObservation { return RuntimeObservation{State: "unknown"} }}
	device = f.ok(t, "alice", "GET", "/devices/device-a", nil, 200)
	task = device["tasks"].([]any)[0].(map[string]any)
	if task["runtimeState"] != "unknown" || task["runtimeObservedAt"] != nil || task["state"] != "registered" {
		t.Fatal("loaded model or admin state implied running process")
	}
	f.s.opts.MountProvider = runtimeProjectionProvider{observe: func(context.Context, Task) RuntimeObservation {
		f.endpointAllowed = false
		return RuntimeObservation{State: "running", ObservedAt: f.now}
	}}
	f.ok(t, "alice", "GET", "/devices/device-a", nil, 404)
}

func TestEmptyQuestionIsGeneralSummaryWithEmptyAnswerAndReferencedTopics(t *testing.T) {
	f := setup(t)
	f.ingest(t, f.event("general-summary"))
	f.s.opts.Provider = providerFunc(func(_ context.Context, _ SummaryRequest, e []Evidence) (Summary, error) {
		return Summary{Title: "一般总结", Summary: "记录了交流。", Topics: []Topic{{Text: "交流", EventRefs: []string{e[0].Ref}}}, Answer: "不应向空问题返回此字段。"}, nil
	})
	result := f.ok(t, "alice", "POST", "/summaries", SummaryRequest{DeviceID: "device-a", From: f.now.Add(-time.Hour * 24), To: f.now}, 201)
	if result["answer"] != "" || result["status"] != "completed" {
		t.Fatal(result)
	}
	f.s.opts.Provider = providerFunc(func(context.Context, SummaryRequest, []Evidence) (Summary, error) {
		return Summary{Title: "标题", Summary: "无引用主题。", Topics: []Topic{}}, nil
	})
	result = f.ok(t, "alice", "POST", "/summaries", SummaryRequest{DeviceID: "device-a", From: f.now.Add(-time.Hour * 24), To: f.now}, 201)
	if result["errorCode"] != "provider_invalid_evidence" {
		t.Fatal(result)
	}
}

func TestDeepSeekGeneralSummaryUsesPlainChinesePolicyAndClearsAnswer(t *testing.T) {
	file := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(file, []byte("test-provider-only-value"), 0600); err != nil {
		t.Fatal(err)
	}
	provider := &DeepSeekProvider{SecretFile: file, Model: "deepseek-flash", Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		policy := body["messages"].([]any)[0].(map[string]any)["content"].(string)
		for _, required := range []string{"通俗简体中文", "不要在正文里输出", "source_history", "原始 UTC 时间段", "一般总结", "不要抱怨未提供问题", "topics 必须非空"} {
			if !strings.Contains(policy, required) {
				t.Errorf("general-summary policy omitted %q", required)
			}
		}
		content := `{"title":"总结","summary":"记录了交流。","topics":[{"text":"交流","eventRefs":["evt:e"]}],"answer":"不应保留","uncertainties":[]}`
		response, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": content}}}})
		return &http.Response{StatusCode: 200, Body: ioNop(response), Header: make(http.Header)}, nil
	})}}
	result, err := provider.Summarize(context.Background(), SummaryRequest{}, []Evidence{{Ref: "evt:e", EventID: "e", Text: "交流记录"}})
	if err != nil || result.Answer != "" {
		t.Fatalf("empty question retained answer: %+v %v", result, err)
	}
}
