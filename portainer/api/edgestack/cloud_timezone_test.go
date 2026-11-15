package edgestack

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func TestSummaryTimezoneDefaultValidationAndPersistence(t *testing.T) {
	f := setup(t)
	f.ingest(t, f.event("timezone"))
	f.s.opts.Provider = providerFunc(func(_ context.Context, request SummaryRequest, e []Evidence) (Summary, error) {
		if request.TimeZone == "" || request.From.Location() != time.UTC || request.To.Location() != time.UTC {
			t.Fatal("timezone was omitted or UTC bounds changed")
		}
		return Summary{Title: "T", Summary: "S", Topics: []Topic{{Text: "S", EventRefs: []string{e[0].Ref}}}}, nil
	})
	request := SummaryRequest{DeviceID: "device-a", From: f.now.Add(-24 * time.Hour), To: f.now}
	for _, name := range []string{"", "America/New_York", "UTC"} {
		request.TimeZone = name
		result := f.ok(t, "alice", "POST", "/summaries", request, 201)
		expected := name
		if expected == "" {
			expected = DefaultSummaryTimeZone
		}
		if result["usedTimeZone"] != expected {
			t.Fatal("used timezone not returned")
		}
		f.s.db.View(func(tx *bolt.Tx) error {
			record, err := read[summaryRecord](tx, "summaries", result["id"].(string))
			if err != nil || record.Request.TimeZone != expected || record.Summary.UsedTimeZone != expected {
				t.Error("used timezone not persisted")
			}
			return err
		})
	}
	for _, name := range []string{"Local", "Mars/Olympus", " Asia/Shanghai", "../etc/passwd"} {
		request.TimeZone = name
		f.ok(t, "alice", "POST", "/summaries", request, 400)
	}
}

func TestDeepSeekUsesLocalCalendarContextAcrossUTCMidnight(t *testing.T) {
	file := filepath.Join(t.TempDir(), "key")
	os.WriteFile(file, []byte("fake-timezone-key"), 0600)
	provider := &DeepSeekProvider{SecretFile: file, Model: "deepseek-flash", Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var payload struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&payload)
		var input map[string]any
		json.Unmarshal([]byte(payload.Messages[1].Content), &input)
		if input["timeZone"] != "Asia/Shanghai" || !strings.HasPrefix(input["localFrom"].(string), "2026-11-16T00:00") || !strings.Contains(payload.Messages[0].Content, "不能按 UTC 日期推断") {
			t.Fatal("UTC dates were used for the user's local today")
		}
		event := input["evidence"].([]any)[0].(map[string]any)
		if event["occurredAt"] != "2026-11-15T17:00:00Z" || event["localTime"] != "2026-11-16T01:00:00+08:00" {
			t.Fatal("event UTC storage or local-time context lost")
		}
		return cloudChatResponse("evt:timezone", "日期已按本地时间解释。"), nil
	})}}
	from := time.Date(2026, 11, 15, 16, 0, 0, 0, time.UTC)
	_, err := provider.Summarize(context.Background(), SummaryRequest{From: from, To: from.Add(2 * time.Hour), Question: "今天发生了什么？", TimeZone: "Asia/Shanghai"}, []Evidence{{Ref: "evt:timezone", Text: "记录", OccurredAt: from.Add(time.Hour)}})
	if err != nil {
		t.Fatal(err)
	}
}
