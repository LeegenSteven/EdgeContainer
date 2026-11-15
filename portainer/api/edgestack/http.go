package edgestack

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	bolt "go.etcd.io/bbolt"
)

type apiError struct {
	Status int
	Code   string
}

func (e *apiError) Error() string        { return e.Code }
func fail(status int, code string) error { return &apiError{status, code} }
func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func route(fn func(http.ResponseWriter, *http.Request) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			var e *apiError
			if errors.As(err, &e) {
				respond(w, e.Status, map[string]string{"errorCode": e.Code})
			} else if errors.Is(err, errNotFound) {
				respond(w, 404, map[string]string{"errorCode": "not_found"})
			} else {
				respond(w, 500, map[string]string{"errorCode": "internal_error"})
			}
		}
	})
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return fail(400, "invalid_json")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fail(400, "invalid_json")
	}
	return nil
}

func (s *Service) Handler() http.Handler {
	r := mux.NewRouter()
	add := func(method, path string, fn func(http.ResponseWriter, *http.Request) error, admin bool) {
		h := route(fn)
		if admin {
			if s.opts.AdminAccess != nil {
				h = s.opts.AdminAccess(h)
			}
		} else if s.opts.Authenticated != nil {
			h = s.opts.Authenticated(h)
		}
		r.Handle("/api/edgestack"+path, h).Methods(method)
	}
	add("GET", "/devices", s.devices, false)
	add("GET", "/devices/{id}", s.device, false)
	add("GET", "/devices/{id}/event-sources", s.sources, false)
	add("GET", "/events", s.events, false)
	add("GET", "/tasks/{id}/models", s.models, false)
	add("GET", "/tasks/{id}/voice-test", s.voiceTest, false)
	add("GET", "/tasks/{id}/voice/{path:.*}", s.voiceProxy, false)
	add("POST", "/tasks/{id}/model-change-preview", s.preview, false)
	add("POST", "/tasks/{id}/model-changes", s.changeModel, false)
	add("GET", "/operations/{id}", s.operation, false)
	add("POST", "/summaries", s.createSummary, false)
	add("GET", "/summaries/{id}", s.getSummary, false)
	for _, bucket := range []string{"devices", "tasks", "sources", "models"} {
		b := bucket
		add("GET", "/admin/"+b, func(w http.ResponseWriter, r *http.Request) error { return s.adminItems(w, r, b) }, true)
		add("POST", "/admin/"+b, func(w http.ResponseWriter, r *http.Request) error { return s.provision(w, r, b) }, true)
		add("GET", "/admin/"+b+"/{id}", func(w http.ResponseWriter, r *http.Request) error { return s.adminItem(w, r, b) }, true)
		if b != "models" {
			add("PATCH", "/admin/"+b+"/{id}", func(w http.ResponseWriter, r *http.Request) error { return s.adminPatch(w, r, b) }, true)
		}
	}

	add("GET", "/admin/access-preview", s.adminAccessPreview, true)
	for _, bucket := range []string{"operations", "audit"} {
		b := bucket
		add("GET", "/admin/"+b, func(w http.ResponseWriter, r *http.Request) error { return s.adminItems(w, r, b) }, true)
		add("GET", "/admin/"+b+"/{id}", func(w http.ResponseWriter, r *http.Request) error { return s.adminItem(w, r, b) }, true)
	}
	add("POST", "/admin/imports/preview", s.importPreview, true)
	add("POST", "/admin/imports/confirm", s.importConfirm, true)
	add("POST", "/admin/models/{id}/validation-preview", s.validationPreview, true)
	add("POST", "/admin/models/{id}/validations", s.validationConfirm, true)
	add("GET", "/admin/models/{id}/validations", s.modelValidations, true)
	add("GET", "/admin/settings/cloud", s.getCloudSettings, true)
	add("POST", "/admin/settings/cloud/test", s.testCloudSettings, true)
	add("PUT", "/admin/settings/cloud", s.updateCloudSettings, true)
	add("PATCH", "/admin/settings/cloud", s.updateCloudSettings, true)
	add("POST", "/admin/settings/cloud/restore-hostfile", s.restoreCloudHostfile, true)
	add("POST", "/admin/ingest-credentials/{id}/rotate", s.rotateCredential, true)
	add("POST", "/admin/ingest-credentials", s.createCredential, true)
	add("GET", "/admin/ingest-credentials", s.credentials, true)
	add("DELETE", "/admin/ingest-credentials/{id}", s.revokeCredential, true)
	add("POST", "/admin/models/{id}/publication", s.modelPublication, true)
	// Ingest authenticates with a separate hashed credential, never a Portainer
	// session, API key, EDGE_KEY or a browser cookie.
	r.Handle("/api/edgestack/ingest/events", route(s.ingestEvents)).Methods("POST")
	r.Handle("/api/edgestack/ingest/status", route(s.ingestStatus)).Methods("POST")
	return r
}

func (s *Service) principal(r *http.Request) (Principal, error) {
	if s.opts.Authenticate == nil {
		return Principal{}, fail(401, "authentication_required")
	}
	p, err := s.opts.Authenticate(r)
	if err != nil || p.UserID <= 0 {
		return Principal{}, fail(401, "authentication_required")
	}
	return p, nil
}
func (s *Service) admin(r *http.Request) error {
	p, e := s.principal(r)
	if e != nil {
		return e
	}
	if !p.Admin {
		return fail(403, "admin_required")
	}
	return nil
}
func (s *Service) canDevice(r *http.Request, p Principal, d Device) bool {
	return !d.Archived && allows(p, d.ACL) && s.opts.EndpointAccess != nil && s.opts.EndpointAccess(r, d.EndpointID)
}
func (s *Service) canTask(r *http.Request, p Principal, tx *bolt.Tx, t Task) bool {
	d, err := read[Device](tx, "devices", t.DeviceID)
	return err == nil && !t.Archived && s.canDevice(r, p, d) && allows(p, t.ACL) && t.Target.EndpointID == d.EndpointID
}
func (s *Service) authorizedTask(r *http.Request, tx *bolt.Tx, id string) (Task, error) {
	p, e := s.principal(r)
	if e != nil {
		return Task{}, e
	}
	t, e := read[Task](tx, "tasks", id)
	if e != nil {
		return t, e
	}
	if !s.canTask(r, p, tx, t) {
		return t, fail(404, "not_found")
	}
	return t, nil
}
func (s *Service) authorizedDevice(r *http.Request, tx *bolt.Tx, id string) (Device, error) {
	p, e := s.principal(r)
	if e != nil {
		return Device{}, e
	}
	d, e := read[Device](tx, "devices", id)
	if e != nil {
		return d, e
	}
	if !s.canDevice(r, p, d) {
		return d, fail(404, "not_found")
	}
	return d, nil
}

func publicTask(t Task) any {
	out := map[string]any{"id": t.ID, "deviceId": t.DeviceID, "name": t.Name, "description": t.Description, "kind": t.Kind, "state": t.State, "runtimeState": "unknown", "revision": t.Revision, "models": nonNil(t.Models), "dataLocations": publicDataLocations(t.DataLocations), "capabilities": nonNil(t.Capabilities)}
	if t.VoiceOrigin != "" && contains(t.Capabilities, "voice-test") {
		out["voiceTestUrl"] = "/api/edgestack/tasks/" + url.PathEscape(t.ID) + "/voice-test"
	}
	return out
}

// Locations are operator registrations. Neither Compose declarations nor a
// registered host path establishes the live container's actual mounted data.
func publicDataLocations(locations []json.RawMessage) []map[string]any {
	out := make([]map[string]any, 0, len(locations))
	for _, raw := range locations {
		var location map[string]any
		if json.Unmarshal(raw, &location) != nil || location == nil {
			var name string
			if json.Unmarshal(raw, &name) != nil {
				continue
			}
			location = map[string]any{"name": name}
		}
		location["verification"] = "registered"
		// Verification fields are server-derived, never operator assertions.
		delete(location, "observedAt")
		delete(location, "verificationReason")
		out = append(out, location)
	}
	return out
}
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func (s *Service) publicDevice(r *http.Request, tx *bolt.Tx, d Device) (any, error) {
	p, err := s.principal(r)
	if err != nil {
		return nil, err
	}
	tasks, err := list[Task](tx, "tasks")
	if err != nil {
		return nil, err
	}
	outTasks := make([]any, 0)
	allowed := map[string]bool{}
	for _, t := range tasks {
		if t.DeviceID == d.ID && s.canTask(r, p, tx, t) {
			outTasks = append(outTasks, publicTask(t))
			allowed[t.ID] = true
		}
	}
	sources, err := list[Source](tx, "sources")
	if err != nil {
		return nil, err
	}
	outSources := make([]Source, 0)
	lastSeen := time.Time{}
	for _, source := range sources {
		if source.DeviceID == d.ID && allowed[source.TaskID] {
			outSources = append(outSources, s.effectiveSource(source))
			if source.LastSeenAt.After(lastSeen) {
				lastSeen = source.LastSeenAt
			}
		}
	}
	events, err := list[Event](tx, "events")
	if err != nil {
		return nil, err
	}
	recent := make([]Event, 0)
	lastInteraction := time.Time{}
	for _, e := range events {
		if e.DeviceID == d.ID && allowed[e.TaskID] && !e.OccurredAt.Before(s.opts.Now().Add(-Retention)) {
			recent = append(recent, e)
			if e.Type != "voice.playback_state" && e.OccurredAt.After(lastInteraction) {
				lastInteraction = e.OccurredAt
			}
		}
	}
	sortEvents(recent)
	if len(recent) > 10 {
		recent = recent[:10]
	}
	online := !lastSeen.IsZero() && s.opts.Now().Sub(lastSeen) < 2*time.Minute
	if s.opts.DeviceStatus != nil {
		online, lastSeen = s.opts.DeviceStatus(r, d.EndpointID)
	}
	return map[string]any{"id": d.ID, "name": d.Name, "description": d.Description, "online": online, "lastSeenAt": lastSeen, "tasks": outTasks, "sources": outSources, "recentEvents": recent, "lastInteractionAt": lastInteraction}, nil
}

func (s *Service) devices(w http.ResponseWriter, r *http.Request) error {
	p, e := s.principal(r)
	if e != nil {
		return e
	}
	items := make([]any, 0)
	err := s.db.View(func(tx *bolt.Tx) error {
		rows, e := list[Device](tx, "devices")
		if e != nil {
			return e
		}
		for _, d := range rows {
			if s.canDevice(r, p, d) {
				v, e := s.publicDevice(r, tx, d)
				if e != nil {
					return e
				}
				items = append(items, v)
			}
		}
		return nil
	})
	if err == nil {
		if err := s.enrichMountLocations(r, items); err != nil {
			return err
		}
		respond(w, 200, map[string]any{"items": items})
	}
	return err
}
func (s *Service) device(w http.ResponseWriter, r *http.Request) error {
	var out any
	err := s.db.View(func(tx *bolt.Tx) error {
		d, e := s.authorizedDevice(r, tx, mux.Vars(r)["id"])
		if e != nil {
			return e
		}
		out, e = s.publicDevice(r, tx, d)
		return e
	})
	if err == nil {
		if err := s.enrichMountLocations(r, []any{out}); err != nil {
			return err
		}
		respond(w, 200, out)
	}
	return err
}
func (s *Service) effectiveSource(source Source) Source {
	if source.Enabled && !source.LastSeenAt.IsZero() && s.opts.Now().Sub(source.LastSeenAt) >= 2*time.Minute {
		source.Status = "disconnected"
	}
	return source
}

func (s *Service) sources(w http.ResponseWriter, r *http.Request) error {
	var out []Source
	var coverage Coverage
	err := s.db.View(func(tx *bolt.Tx) error {
		d, e := s.authorizedDevice(r, tx, mux.Vars(r)["id"])
		if e != nil {
			return e
		}
		_, out, coverage, e = s.query(r, tx, d.ID, s.opts.Now().Add(-Retention), s.opts.Now())
		return e
	})
	if err == nil {
		respond(w, 200, map[string]any{"items": out, "coverage": coverage})
	}
	return err
}

func (s *Service) window(r *http.Request) (time.Time, time.Time, error) {
	to := s.opts.Now().UTC()
	from := to.Add(-24 * time.Hour)
	var err error
	if v := r.URL.Query().Get("from"); v != "" {
		from, err = time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return from, to, fail(400, "invalid_time_range")
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		to, err = time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return from, to, fail(400, "invalid_time_range")
		}
	}
	if !from.Before(to) || to.Sub(from) > Retention || to.After(s.opts.Now().Add(time.Minute)) {
		return from, to, fail(400, "invalid_time_range")
	}
	return from, to, nil
}
func sortEvents(items []Event) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].OccurredAt.Equal(items[j].OccurredAt) {
			return items[i].EventID > items[j].EventID
		}
		return items[i].OccurredAt.After(items[j].OccurredAt)
	})
}

func (s *Service) query(r *http.Request, tx *bolt.Tx, deviceID string, from, to time.Time) ([]Event, []Source, Coverage, error) {
	c := Coverage{From: from, To: to, RetentionDays: 7, Gaps: []string{}}
	d, e := s.authorizedDevice(r, tx, deviceID)
	if e != nil {
		return nil, nil, c, e
	}
	p, e := s.principal(r)
	if e != nil {
		return nil, nil, c, e
	}
	tasks, e := list[Task](tx, "tasks")
	if e != nil {
		return nil, nil, c, e
	}
	allowed := map[string]bool{}
	for _, t := range tasks {
		if t.DeviceID == d.ID && s.canTask(r, p, tx, t) {
			allowed[t.ID] = true
		}
	}
	sources, e := list[Source](tx, "sources")
	if e != nil {
		return nil, nil, c, e
	}
	outSources := make([]Source, 0)
	for _, source := range sources {
		if source.DeviceID == d.ID && allowed[source.TaskID] {
			source = s.effectiveSource(source)
			outSources = append(outSources, source)
			// Model receipts describe runtime identity, not conversation or
			// camera coverage. Keep their health visible without counting them
			// as producers or letting their liveness imply recorded history.
			if !strings.HasPrefix(source.Type, "voice") && !strings.HasPrefix(source.Type, "camera") {
				continue
			}
			c.SourceCount++
			gap := ""
			if !source.Enabled {
				gap = "source_disabled"
			} else if source.Status == "error" {
				gap = "source_error"
			} else if source.Status != "connected" && source.Status != "healthy" && source.Status != "streaming" {
				gap = "source_disconnected"
			} else {
				c.AvailableSources++
			}
			if gap != "" && !contains(c.Gaps, gap) {
				c.Gaps = append(c.Gaps, gap)
			}
			var history struct {
				Complete     *bool      `json:"complete"`
				GapCount     int        `json:"gapCount"`
				From         *time.Time `json:"from"`
				To           *time.Time `json:"to"`
				ObservedFrom *time.Time `json:"observedFrom"`
				ObservedTo   *time.Time `json:"observedTo"`
			}
			historyGap := ""
			if len(source.Coverage) == 0 || json.Unmarshal(source.Coverage, &history) != nil || history.Complete == nil {
				historyGap = "source_history_unknown"
			} else if !*history.Complete || history.GapCount > 0 {
				historyGap = "source_history_incomplete"
			} else {
				start, end := history.From, history.To
				if start == nil {
					start = history.ObservedFrom
				}
				if end == nil {
					end = history.ObservedTo
				}
				if start == nil || end == nil {
					historyGap = "source_history_unknown"
				} else if start.After(from) || end.Before(to) {
					historyGap = "source_window_incomplete"
				}
			}
			if historyGap != "" && !contains(c.Gaps, historyGap) {
				c.Gaps = append(c.Gaps, historyGap)
			}
		}
	}
	if c.SourceCount == 0 {
		c.Gaps = append(c.Gaps, "no_source_configured")
	}
	if from.Before(s.opts.Now().Add(-Retention)) {
		c.Gaps = append(c.Gaps, "outside_retention")
	}
	rows, e := list[Event](tx, "events")
	if e != nil {
		return nil, nil, c, e
	}
	items := make([]Event, 0)
	for _, event := range rows {
		if event.DeviceID == deviceID && allowed[event.TaskID] && !event.OccurredAt.Before(from) && event.OccurredAt.Before(to) && !event.OccurredAt.Before(s.opts.Now().Add(-Retention)) {
			items = append(items, event)
		}
	}
	sortEvents(items)
	c.EventCount = len(items)
	c.Complete = len(c.Gaps) == 0
	return items, outSources, c, nil
}

func (s *Service) events(w http.ResponseWriter, r *http.Request) error {
	from, to, e := s.window(r)
	if e != nil {
		return e
	}
	id := r.URL.Query().Get("deviceId")
	if id == "" {
		return fail(400, "device_required")
	}
	var items []Event
	var coverage Coverage
	err := s.db.View(func(tx *bolt.Tx) error {
		var err error
		items, _, coverage, err = s.query(r, tx, id, from, to)
		return err
	})
	if err != nil {
		return err
	}
	types := []string{}
	if value := r.URL.Query().Get("type"); value != "" {
		for _, kind := range strings.Split(value, ",") {
			kind = strings.TrimSpace(kind)
			if !contains([]string{"voice.user_utterance", "voice.assistant_reply", "voice.playback_state", "camera.observation"}, kind) {
				return fail(400, "invalid_event_type")
			}
			types = append(types, kind)
		}
	}
	if len(types) > 0 || r.URL.Query().Get("sessionId") != "" {
		session := r.URL.Query().Get("sessionId")
		filtered := make([]Event, 0, len(items))
		for _, event := range items {
			if (len(types) == 0 || contains(types, event.Type)) && (session == "" || event.SessionID == session) {
				filtered = append(filtered, event)
			}
		}
		items = filtered
	}
	filteredCount := len(items)
	// The opaque cursor is the final event ID in the previous page. It is only
	// resolved after scope and time-window filtering, so it cannot expose data.
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		found := false
		for i, e := range items {
			if e.EventID == cursor {
				items = items[i+1:]
				found = true
				break
			}
		}
		if !found {
			return fail(400, "invalid_cursor")
		}
	}
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 || n > 500 {
			return fail(400, "invalid_limit")
		}
		limit = n
	}
	next := ""
	if len(items) > limit {
		next = items[limit-1].EventID
		items = items[:limit]
	}
	respond(w, 200, map[string]any{"items": items, "nextCursor": next, "coverage": coverage, "filteredCount": filteredCount})
	return nil
}

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var digest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

func validID(v string) bool { return identifier.MatchString(v) }

func (s *Service) adminList(w http.ResponseWriter, r *http.Request, bucket string) error {
	if e := s.admin(r); e != nil {
		return e
	}
	items := make([]json.RawMessage, 0)
	e := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucket)).ForEach(func(_, v []byte) error { items = append(items, append(json.RawMessage{}, v...)); return nil })
	})
	if e == nil {
		respond(w, 200, map[string]any{"items": items})
	}
	return e
}
func (s *Service) provision(w http.ResponseWriter, r *http.Request, bucket string) error {
	if err := s.admin(r); err != nil {
		return err
	}
	s.modelMu.Lock()
	defer s.modelMu.Unlock()
	var out any
	err := s.db.Update(func(tx *bolt.Tx) error {
		switch bucket {
		case "devices":
			var d Device
			if err := decode(w, r, &d); err != nil {
				return err
			}
			if _, e := read[Device](tx, bucket, d.ID); e == nil {
				return fail(409, "object_exists")
			}
			if !validID(d.ID) || strings.TrimSpace(d.Name) == "" || d.EndpointID <= 0 || !s.opts.EndpointAccess(r, d.EndpointID) {
				return fail(400, "invalid_device")
			}
			if e := s.validateBusinessACL(d.ACL); e != nil {
				return e
			}
			d.MetadataVersion = 1
			out = d
			if e := put(tx, bucket, d.ID, d); e != nil {
				return e
			}
			return s.auditTx(tx, r, "device.create", bucket, d.ID, []string{"registration", "acl"})
		case "tasks":
			var t Task
			if err := decode(w, r, &t); err != nil {
				return err
			}
			if _, e := read[Task](tx, bucket, t.ID); e == nil {
				return fail(409, "object_exists")
			}
			d, e := read[Device](tx, "devices", t.DeviceID)
			if e != nil {
				return fail(400, "device_not_found")
			}
			if !validID(t.ID) || t.Name == "" || t.Kind == "" || t.Target.EndpointID != d.EndpointID || t.Target.StackID <= 0 || !validID(t.Target.ServiceID) {
				return fail(400, "invalid_task")
			}
			if e := validateTaskMetadata(t); e != nil {
				return e
			}
			if e := s.validateBusinessACL(t.ACL); e != nil {
				return e
			}
			if s.opts.ValidateTask != nil {
				if e := s.opts.ValidateTask(r.Context(), t); e != nil {
					return fail(400, "invalid_task_target")
				}
			}
			rows, e := list[Task](tx, bucket)
			if e != nil {
				return e
			}
			for _, other := range rows {
				if other.Target.StackID == t.Target.StackID {
					return fail(409, "stack_already_registered")
				}
			}
			t.MetadataVersion = 1
			t.Revision = 1
			for i := range t.Models {
				t.Models[i].ObservedPackageID = ""
				t.Models[i].ObservedModelID = ""
				t.Models[i].ObservedModelHash = ""
				t.Models[i].ObservedAt = time.Time{}
				t.Models[i].State = "unobserved"
			}
			out = t
			if e := put(tx, bucket, t.ID, t); e != nil {
				return e
			}
			return s.auditTx(tx, r, "task.create", bucket, t.ID, []string{"registration", "acl"})
		case "sources":
			var source Source
			if err := decode(w, r, &source); err != nil {
				return err
			}
			if _, e := read[Source](tx, bucket, source.ID); e == nil {
				return fail(409, "object_exists")
			}
			task, e := read[Task](tx, "tasks", source.TaskID)
			if e != nil || task.DeviceID != source.DeviceID || !validID(source.ID) || source.Name == "" || !validSourceType(source.Type) {
				return fail(400, "invalid_source")
			}
			source.MetadataVersion = 1
			source.Status = "unconfigured"
			source.LastSeenAt = time.Time{}
			source.LastEventAt = time.Time{}
			source.Checkpoint = nil
			source.Coverage = nil
			source.ErrorCode = ""
			out = source
			if e := put(tx, bucket, source.ID, source); e != nil {
				return e
			}
			return s.auditTx(tx, r, "source.create", bucket, source.ID, []string{"registration"})
		case "models":
			var model ModelPackage
			if err := decode(w, r, &model); err != nil {
				return err
			}
			if _, e := read[ModelPackage](tx, bucket, model.ID); e == nil {
				return fail(409, "package_immutable")
			}
			if e := validateModelBody(model); e != nil {
				return e
			}
			if model.Published {
				return fail(400, "model_must_start_draft")
			}
			out = model
			if e := put(tx, bucket, model.ID, model); e != nil {
				return e
			}
			if e := put(tx, "model-policies", model.ID, s.modelPolicy(tx, model.ID)); e != nil {
				return e
			}
			return s.auditTx(tx, r, "model.create", bucket, model.ID, []string{"immutablePackage"})
		}
		return errNotFound
	})
	if err == nil {
		respond(w, 200, out)
	}
	return err
}
