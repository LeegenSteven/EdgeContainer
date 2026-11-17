package edgestack

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	bolt "go.etcd.io/bbolt"
)

func (s *Service) auditTx(tx *bolt.Tx, r *http.Request, action, kind, id string, fields []string) error {
	actor := 0
	if r != nil {
		p, err := s.principal(r)
		if err != nil {
			return err
		}
		actor = p.UserID
	}
	entry := AuditEntry{ID: newID(), ActorUserID: actor, Action: action, ResourceType: kind, ResourceID: redactText(id, nil), OccurredAt: s.opts.Now().UTC(), Result: "succeeded", Fields: []string{}}
	if strings.HasSuffix(action, ".failed") {
		entry.Result = "failed"
	}
	for _, field := range fields {
		if len(field) <= 80 {
			entry.Fields = append(entry.Fields, redactText(field, nil))
		}
	}
	if kind == "tasks" {
		entry.TaskID = id
		if task, err := read[Task](tx, "tasks", id); err == nil {
			entry.DeviceID = task.DeviceID
		}
	}
	if kind == "devices" {
		entry.DeviceID = id
	}
	if kind == "sources" {
		if source, err := read[Source](tx, "sources", id); err == nil {
			entry.DeviceID = source.DeviceID
			entry.TaskID = source.TaskID
		}
	}
	return put(tx, "audit", entry.ID, entry)
}
func (s *Service) RecordAudit(r *http.Request, action, kind, id string, fields []string) error {
	return s.db.Update(func(tx *bolt.Tx) error { return s.auditTx(tx, r, action, kind, id, fields) })
}
func (s *Service) validateBusinessACL(acl ACL) error {
	for _, id := range append(append([]int{}, acl.UserIDs...), acl.TeamIDs...) {
		if id <= 0 {
			return fail(400, "invalid_acl_subject")
		}
	}
	if s.opts.ValidateACL != nil {
		if err := s.opts.ValidateACL(acl); err != nil {
			return fail(400, "invalid_acl_subject")
		}
	}
	return nil
}
func version(v int) int {
	if v < 1 {
		return 1
	}
	return v
}
func (s *Service) modelPolicy(tx *bolt.Tx, id string) ModelPolicy {
	p, err := read[ModelPolicy](tx, "model-policies", id)
	if err != nil {
		return ModelPolicy{MetadataVersion: 1, ValidationStatus: "draft", AllowedTaskIDs: []string{}, ValidatedScopes: []PublicationScope{}}
	}
	return p
}
func (s *Service) modelAdmin(tx *bolt.Tx, m ModelPackage) map[string]any {
	b, _ := json.Marshal(m)
	out := map[string]any{}
	_ = json.Unmarshal(b, &out)
	p := s.modelPolicy(tx, m.ID)
	extra, _ := json.Marshal(p)
	_ = json.Unmarshal(extra, &out)
	return out
}
func scopeFor(task Task, device Device) PublicationScope {
	return PublicationScope{TaskKind: task.Kind, Architecture: device.Architecture, RuntimeBackend: task.RuntimeBackend, TemplateID: task.TemplateID}
}
func (s *Service) policyAllows(tx *bolt.Tx, task Task, device Device, model ModelPackage, principal Principal) bool {
	if !s.deliveryArtifactAllows(tx, task, device, model, principal) {
		return false
	}
	p := s.modelPolicy(tx, model.ID)
	if p.LegacyScoped && contains(p.AllowedTaskIDs, task.ID) {
		if scope, ok := p.LegacyTaskScopes[task.ID]; ok && scope == scopeFor(task, device) {
			return true
		}
	}
	for _, scope := range p.ValidatedScopes {
		if scope == scopeFor(task, device) {
			return true
		}
	}
	return false
}
func (s *Service) migrateAdminSchema() error {
	return s.db.Update(func(tx *bolt.Tx) error {
		for _, kind := range []string{"devices", "tasks", "sources"} {
			bucket := tx.Bucket([]byte(kind))
			rows := map[string]json.RawMessage{}
			if err := bucket.ForEach(func(k, v []byte) error { rows[string(k)] = append(json.RawMessage{}, v...); return nil }); err != nil {
				return err
			}
			for id, raw := range rows {
				var item map[string]any
				if err := json.Unmarshal(raw, &item); err != nil {
					return err
				}
				changed := false
				if n, ok := item["metadataVersion"].(float64); !ok || n < 1 {
					item["metadataVersion"] = 1
					changed = true
				}
				if _, exists := item["archived"]; !exists {
					item["archived"] = false
					changed = true
				}
				if changed {
					if err := put(tx, kind, id, item); err != nil {
						return err
					}
				}
			}
		}
		if tx.Bucket([]byte("admin-meta")).Get([]byte("schema")) != nil {
			return nil
		}
		tasks, err := list[Task](tx, "tasks")
		if err != nil {
			return err
		}
		models, err := list[ModelPackage](tx, "models")
		if err != nil {
			return err
		}
		for _, m := range models {
			p := ModelPolicy{MetadataVersion: 1, ValidationStatus: "draft", AllowedTaskIDs: []string{}, LegacyTaskScopes: map[string]PublicationScope{}, ValidatedScopes: []PublicationScope{}}
			if m.Published {
				p.LegacyScoped = true
				p.ValidationStatus = "legacy_scoped"
				for _, t := range tasks {
					d, e := read[Device](tx, "devices", t.DeviceID)
					if e == nil && compatibleBody(t, d, m) {
						p.AllowedTaskIDs = append(p.AllowedTaskIDs, t.ID)
						p.LegacyTaskScopes[t.ID] = scopeFor(t, d)
					}
				}
			}
			if err := put(tx, "model-policies", m.ID, p); err != nil {
				return err
			}
		}
		return tx.Bucket([]byte("admin-meta")).Put([]byte("schema"), []byte("1"))
	})
}

func pageArgs(r *http.Request) (int, int, error) {
	page, size := 1, 25
	for key, dest := range map[string]*int{"page": &page, "pageSize": &size} {
		if v := r.URL.Query().Get(key); v != "" {
			n, e := strconv.Atoi(v)
			if e != nil || n < 1 {
				return 0, 0, fail(400, "invalid_pagination")
			}
			*dest = n
		}
	}
	if page > 1000000 || size > 100 {
		return 0, 0, fail(400, "invalid_pagination")
	}
	return page, size, nil
}
func (s *Service) adminItems(w http.ResponseWriter, r *http.Request, kind string) error {
	if err := s.admin(r); err != nil {
		return err
	}
	page, size, err := pageArgs(r)
	if err != nil {
		return err
	}
	items := []map[string]any{}
	err = s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(kind)).ForEach(func(_, raw []byte) error {
			var item map[string]any
			if kind == "credentials" {
				var row credentialRecord
				if err := json.Unmarshal(raw, &row); err != nil {
					return err
				}
				b, _ := json.Marshal(row.Credential)
				_ = json.Unmarshal(b, &item)
			} else {
				if err := json.Unmarshal(raw, &item); err != nil {
					return err
				}
			}
			if kind == "models" {
				var model ModelPackage
				if err := json.Unmarshal(raw, &model); err != nil {
					return err
				}
				item = s.modelAdmin(tx, model)
			}
			q := r.URL.Query()
			for _, queryKey := range []string{"deviceId", "taskId", "sourceId", "endpointId", "architecture", "status", "result", "validationStatus", "published", "archived", "enabled", "revoked", "actorUserId", "resourceType", "resourceId", "packageId"} {
				value := q.Get(queryKey)
				if value == "" {
					continue
				}
				key := queryKey
				if kind == "credentials" && (key == "deviceId" || key == "taskId" || key == "sourceId") {
					field := map[string]string{"deviceId": "deviceIds", "taskId": "taskIds", "sourceId": "sourceIds"}[key]
					present := false
					if ids, ok := item[field].([]any); ok {
						for _, id := range ids {
							if fmt.Sprint(id) == value {
								present = true
							}
						}
					}
					if !present {
						return nil
					}
					continue
				}
				if key == "actorUserId" && kind == "credentials" {
					key = "createdByUserId"
				}
				if key == "status" && kind == "tasks" {
					key = "state"
				}
				if key == "status" && kind == "models" {
					key = "validationStatus"
				}
				if fmt.Sprint(item[key]) != value {
					return nil
				}
			}
			if query := strings.ToLower(strings.TrimSpace(q.Get("q"))); query != "" {
				haystack := strings.ToLower(fmt.Sprint(item["id"], " ", item["name"], " ", item["description"], " ", item["action"]))
				if !strings.Contains(haystack, query) {
					return nil
				}
			}
			stamp := fmt.Sprint(item["occurredAt"])
			if stamp == "<nil>" {
				stamp = fmt.Sprint(item["createdAt"])
			}
			for _, key := range []string{"from", "to"} {
				if value := q.Get(key); value != "" {
					boundary, e := time.Parse(time.RFC3339Nano, value)
					if e != nil {
						return fail(400, "invalid_time_range")
					}
					actual, e := time.Parse(time.RFC3339Nano, stamp)
					if e != nil {
						return nil
					}
					if key == "from" && actual.Before(boundary) || key == "to" && !actual.Before(boundary) {
						return nil
					}
				}
			}
			items = append(items, item)
			return nil
		})
	})
	if err != nil {
		return err
	}
	sort.Slice(items, func(i, j int) bool {
		if kind == "operations" || kind == "audit" || kind == "model-validations" {
			a, b := fmt.Sprint(items[i]["createdAt"]), fmt.Sprint(items[j]["createdAt"])
			if kind == "audit" {
				a, b = fmt.Sprint(items[i]["occurredAt"]), fmt.Sprint(items[j]["occurredAt"])
			}
			if a != b {
				return a > b
			}
		}
		return fmt.Sprint(items[i]["id"]) < fmt.Sprint(items[j]["id"])
	})
	total := len(items)
	start := (page - 1) * size
	if start > total {
		start = total
	}
	end := start + size
	if end > total {
		end = total
	}
	respond(w, 200, map[string]any{"items": items[start:end], "totalCount": total, "page": page, "pageSize": size})
	return nil
}
func (s *Service) adminItem(w http.ResponseWriter, r *http.Request, kind string) error {
	if err := s.admin(r); err != nil {
		return err
	}
	var item any
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket([]byte(kind)).Get([]byte(mux.Vars(r)["id"]))
		if raw == nil {
			return errNotFound
		}
		if kind == "models" {
			var m ModelPackage
			if e := json.Unmarshal(raw, &m); e != nil {
				return e
			}
			item = s.modelAdmin(tx, m)
			return nil
		}
		return json.Unmarshal(raw, &item)
	})
	if err == nil {
		respond(w, 200, item)
	}
	return err
}

func (s *Service) adminAccessPreview(w http.ResponseWriter, r *http.Request) error {
	if err := s.admin(r); err != nil {
		return err
	}
	userID, err := strconv.Atoi(r.URL.Query().Get("userId"))
	if err != nil || userID <= 0 {
		return fail(400, "invalid_user_id")
	}
	out := map[string]any{"userId": userID, "deviceId": r.URL.Query().Get("deviceId"), "taskId": r.URL.Query().Get("taskId"), "nativeEndpointAccess": nil, "effectiveAccess": false, "missing": []string{}}
	err = s.db.View(func(tx *bolt.Tx) error {
		device, e := read[Device](tx, "devices", r.URL.Query().Get("deviceId"))
		if e != nil {
			return e
		}
		if s.opts.InspectAccess == nil {
			out["missing"] = []string{"native_permission_unavailable"}
			return nil
		}
		principal, native, e := s.opts.InspectAccess(userID, device.EndpointID)
		if e != nil {
			return fail(400, "invalid_acl_subject")
		}
		deviceAllowed := allows(principal, device.ACL) && !device.Archived
		taskAllowed := true
		if id := r.URL.Query().Get("taskId"); id != "" {
			task, e := read[Task](tx, "tasks", id)
			if e != nil || task.DeviceID != device.ID {
				return errNotFound
			}
			taskAllowed = allows(principal, task.ACL) && !task.Archived
		}
		missing := []string{}
		if !deviceAllowed {
			missing = append(missing, "device_business_permission")
		}
		if !taskAllowed {
			missing = append(missing, "task_business_permission")
		}
		if !native {
			missing = append(missing, "native_endpoint_permission")
		}
		out["deviceBusinessAccess"] = deviceAllowed
		out["taskBusinessAccess"] = taskAllowed
		out["nativeEndpointAccess"] = native
		out["effectiveAccess"] = deviceAllowed && taskAllowed && native
		out["missing"] = missing
		return nil
	})
	if err == nil {
		respond(w, 200, out)
	}
	return err
}

type metadataPatch struct {
	MetadataVersion int                `json:"metadataVersion"`
	Name            *string            `json:"name"`
	Description     *string            `json:"description"`
	Archived        *bool              `json:"archived"`
	UserIDs         *[]int             `json:"userIds"`
	TeamIDs         *[]int             `json:"teamIds"`
	DataLocations   *[]json.RawMessage `json:"dataLocations"`
	Capabilities    *[]string          `json:"capabilities"`
	ModelEnvKeys    *[]string          `json:"modelEnvKeys"`
	VoiceOrigin     *string            `json:"voiceOrigin"`
	VoiceTokenFile  *string            `json:"voiceTokenFile"`
	TestTask        *bool              `json:"testTask"`
	RuntimeBackend  *string            `json:"runtimeBackend"`
	TemplateID      *string            `json:"templateId"`
	Enabled         *bool              `json:"enabled"`
}

func (s *Service) applyPatch(tx *bolt.Tx, kind, id string, p metadataPatch) (any, []string, error) {
	fields := []string{}
	var out any
	switch kind {
	case "devices":
		item, e := read[Device](tx, kind, id)
		if e != nil {
			return nil, nil, e
		}
		if p.MetadataVersion != version(item.MetadataVersion) {
			return nil, nil, fail(409, "metadata_version_conflict")
		}
		if p.DataLocations != nil || p.Capabilities != nil || p.ModelEnvKeys != nil || p.VoiceOrigin != nil || p.VoiceTokenFile != nil || p.TestTask != nil || p.RuntimeBackend != nil || p.TemplateID != nil || p.Enabled != nil {
			return nil, nil, fail(400, "invalid_patch_field")
		}
		if p.Name != nil {
			item.Name = *p.Name
			fields = append(fields, "name")
		}
		if p.Description != nil {
			item.Description = *p.Description
			fields = append(fields, "description")
		}
		if p.Archived != nil {
			item.Archived = *p.Archived
			fields = append(fields, "archived")
		}
		if p.UserIDs != nil {
			item.UserIDs = *p.UserIDs
			fields = append(fields, "userIds")
		}
		if p.TeamIDs != nil {
			item.TeamIDs = *p.TeamIDs
			fields = append(fields, "teamIds")
		}
		if strings.TrimSpace(item.Name) == "" {
			return nil, nil, fail(400, "invalid_device")
		}
		if e := s.validateBusinessACL(item.ACL); e != nil {
			return nil, nil, e
		}
		item.MetadataVersion = version(item.MetadataVersion) + 1
		out = item
	case "tasks":
		item, e := read[Task](tx, kind, id)
		if e != nil {
			return nil, nil, e
		}
		if p.MetadataVersion != version(item.MetadataVersion) {
			return nil, nil, fail(409, "metadata_version_conflict")
		}
		if p.Enabled != nil {
			return nil, nil, fail(400, "invalid_patch_field")
		}
		configuration := p.Capabilities != nil && !reflect.DeepEqual(*p.Capabilities, item.Capabilities) || p.ModelEnvKeys != nil && !reflect.DeepEqual(*p.ModelEnvKeys, item.ModelEnvKeys) || p.VoiceOrigin != nil && *p.VoiceOrigin != item.VoiceOrigin || p.VoiceTokenFile != nil && *p.VoiceTokenFile != item.VoiceTokenFile || p.TestTask != nil && *p.TestTask != item.TestTask || p.RuntimeBackend != nil && *p.RuntimeBackend != item.RuntimeBackend || p.TemplateID != nil && *p.TemplateID != item.TemplateID
		if configuration {
			ops, e := list[Operation](tx, "operations")
			if e != nil {
				return nil, nil, e
			}
			for _, op := range ops {
				if op.TaskID == id && (op.Status == "dispatching" || op.Status == "awaiting_observation") {
					return nil, nil, fail(409, "model_change_in_progress")
				}
			}
		}
		if p.Name != nil {
			item.Name = *p.Name
			fields = append(fields, "name")
		}
		if p.Description != nil {
			item.Description = *p.Description
			fields = append(fields, "description")
		}
		if p.Archived != nil {
			item.Archived = *p.Archived
			fields = append(fields, "archived")
		}
		if p.UserIDs != nil {
			item.UserIDs = *p.UserIDs
			fields = append(fields, "userIds")
		}
		if p.TeamIDs != nil {
			item.TeamIDs = *p.TeamIDs
			fields = append(fields, "teamIds")
		}
		if p.DataLocations != nil {
			item.DataLocations = *p.DataLocations
			fields = append(fields, "dataLocations")
		}
		if p.Capabilities != nil {
			item.Capabilities = *p.Capabilities
			fields = append(fields, "capabilities")
		}
		if p.ModelEnvKeys != nil {
			item.ModelEnvKeys = *p.ModelEnvKeys
			fields = append(fields, "modelEnvKeys")
		}
		if p.VoiceOrigin != nil {
			item.VoiceOrigin = *p.VoiceOrigin
			fields = append(fields, "voiceOrigin")
		}
		if p.VoiceTokenFile != nil {
			item.VoiceTokenFile = *p.VoiceTokenFile
			fields = append(fields, "voiceTokenFile")
		}
		if p.TestTask != nil {
			item.TestTask = *p.TestTask
			fields = append(fields, "testTask")
		}
		if p.RuntimeBackend != nil {
			item.RuntimeBackend = *p.RuntimeBackend
			fields = append(fields, "runtimeBackend")
		}
		if p.TemplateID != nil {
			item.TemplateID = *p.TemplateID
			fields = append(fields, "templateId")
		}
		if strings.TrimSpace(item.Name) == "" {
			return nil, nil, fail(400, "invalid_task")
		}
		if e := s.validateBusinessACL(item.ACL); e != nil {
			return nil, nil, e
		}
		if p.DataLocations != nil || configuration {
			if e := validateTaskMetadata(item); e != nil {
				return nil, nil, e
			}
		}
		if configuration {
			item.Revision++
		}
		item.MetadataVersion = version(item.MetadataVersion) + 1
		out = item
	case "sources":
		item, e := read[Source](tx, kind, id)
		if e != nil {
			return nil, nil, e
		}
		if p.MetadataVersion != version(item.MetadataVersion) {
			return nil, nil, fail(409, "metadata_version_conflict")
		}
		if p.UserIDs != nil || p.TeamIDs != nil || p.DataLocations != nil || p.Capabilities != nil || p.ModelEnvKeys != nil || p.VoiceOrigin != nil || p.VoiceTokenFile != nil || p.TestTask != nil || p.RuntimeBackend != nil || p.TemplateID != nil {
			return nil, nil, fail(400, "invalid_patch_field")
		}
		if p.Name != nil {
			item.Name = *p.Name
			fields = append(fields, "name")
		}
		if p.Description != nil {
			item.Description = *p.Description
			fields = append(fields, "description")
		}
		if p.Archived != nil {
			item.Archived = *p.Archived
			fields = append(fields, "archived")
		}
		if p.Enabled != nil {
			item.Enabled = *p.Enabled
			fields = append(fields, "enabled")
		}
		if strings.TrimSpace(item.Name) == "" {
			return nil, nil, fail(400, "invalid_source")
		}
		item.MetadataVersion = version(item.MetadataVersion) + 1
		out = item
	default:
		return nil, nil, errNotFound
	}
	if len(fields) == 0 {
		return nil, nil, fail(400, "empty_patch")
	}
	if err := put(tx, kind, id, out); err != nil {
		return nil, nil, err
	}
	return out, fields, nil
}
func (s *Service) adminPatch(w http.ResponseWriter, r *http.Request, kind string) error {
	if err := s.admin(r); err != nil {
		return err
	}
	var p metadataPatch
	if err := decode(w, r, &p); err != nil {
		return err
	}
	if p.MetadataVersion < 1 {
		return fail(400, "metadata_version_required")
	}
	s.modelMu.Lock()
	defer s.modelMu.Unlock()
	var out any
	err := s.db.Update(func(tx *bolt.Tx) error {
		var fields []string
		var e error
		out, fields, e = s.applyPatch(tx, kind, mux.Vars(r)["id"], p)
		if e != nil {
			return e
		}
		return s.auditTx(tx, r, "metadata.update", kind, mux.Vars(r)["id"], fields)
	})
	if err == nil {
		respond(w, 200, out)
	}
	return err
}

func changedFields(a, b any) []string {
	rawA, _ := json.Marshal(a)
	rawB, _ := json.Marshal(b)
	var left, right map[string]any
	_ = json.Unmarshal(rawA, &left)
	_ = json.Unmarshal(rawB, &right)
	fields := []string{}
	for key, value := range right {
		if !reflect.DeepEqual(left[key], value) {
			fields = append(fields, key)
		}
	}
	sort.Strings(fields)
	return fields
}
