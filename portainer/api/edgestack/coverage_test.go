package edgestack

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func TestCoverageExcludesModelObserversAndRequiresWholeWindow(t *testing.T) {
	f := setup(t)
	from, to := f.now.Add(-24*time.Hour), f.now
	if err := f.s.db.Update(func(tx *bolt.Tx) error {
		return put(tx, "sources", "observer-a", Source{ID: "observer-a", DeviceID: "device-a", TaskID: "task-a", Name: "Model observer", Type: "model.observer", Enabled: true, Status: "error", LastSeenAt: f.now})
	}); err != nil {
		t.Fatal(err)
	}
	query := func() Coverage {
		t.Helper()
		var c Coverage
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("X-Test-User", "alice")
		if err := f.s.db.View(func(tx *bolt.Tx) error {
			_, sources, coverage, e := f.s.query(r, tx, "device-a", from, to)
			if len(sources) != 2 {
				t.Fatalf("observer health disappeared: %v", sources)
			}
			c = coverage
			return e
		}); err != nil {
			t.Fatal(err)
		}
		return c
	}
	setCoverage := func(coverage any) {
		t.Helper()
		if err := f.s.db.Update(func(tx *bolt.Tx) error {
			s, e := read[Source](tx, "sources", "source-a")
			if e != nil {
				return e
			}
			s.Status = "healthy"
			s.LastSeenAt = f.now
			s.Coverage = nil
			if coverage != nil {
				b, e := json.Marshal(coverage)
				if e != nil {
					return e
				}
				s.Coverage = b
			}
			return put(tx, "sources", s.ID, s)
		}); err != nil {
			t.Fatal(err)
		}
	}
	setCoverage(nil)
	c := query()
	if c.Complete || c.SourceCount != 1 || c.AvailableSources != 1 || !contains(c.Gaps, "source_history_unknown") || contains(c.Gaps, "source_error") {
		t.Fatalf("observer polluted producer coverage: %+v", c)
	}
	setCoverage(map[string]any{"complete": true, "observedFrom": from.Add(time.Hour), "observedTo": to})
	if c := query(); c.Complete || !contains(c.Gaps, "source_window_incomplete") {
		t.Fatal(c)
	}
	setCoverage(map[string]any{"complete": true, "from": from, "to": to.Add(-time.Minute)})
	if c := query(); c.Complete || !contains(c.Gaps, "source_window_incomplete") {
		t.Fatal(c)
	}
	setCoverage(map[string]any{"complete": true, "observedFrom": from, "observedTo": to})
	if c := query(); !c.Complete || len(c.Gaps) != 0 {
		t.Fatalf("full producer history rejected: %+v", c)
	}
	if err := f.s.db.Update(func(tx *bolt.Tx) error {
		s, e := read[Source](tx, "sources", "source-a")
		if e != nil {
			return e
		}
		s.Type = "model.observer"
		return put(tx, "sources", s.ID, s)
	}); err != nil {
		t.Fatal(err)
	}
	if c := query(); c.Complete || c.SourceCount != 0 || c.AvailableSources != 0 || !contains(c.Gaps, "no_source_configured") {
		t.Fatalf("model-only device reported conversation coverage: %+v", c)
	}
}

func TestPublicDataLocationsNeverClaimVerifiedMounts(t *testing.T) {
	task := Task{DataLocations: []json.RawMessage{json.RawMessage(`{"name":"data","path":"/data","verification":"actual"}`), json.RawMessage(`"/legacy/path"`)}}
	public := publicTask(task).(map[string]any)
	locations := public["dataLocations"].([]map[string]any)
	if len(locations) != 2 || locations[0]["verification"] != "registered" || locations[0]["path"] != "/data" || locations[1]["verification"] != "registered" || locations[1]["name"] != "/legacy/path" {
		t.Fatal(locations)
	}
	if string(task.DataLocations[0]) != `{"name":"data","path":"/data","verification":"actual"}` {
		t.Fatal("public projection mutated operator records")
	}
}
