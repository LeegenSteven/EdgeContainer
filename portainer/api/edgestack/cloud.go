package edgestack

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

const DeepSeekModelsURL = "https://api.deepseek.com/models"

type CloudOptions struct {
	SecretDir string
	Client    *http.Client
}

type cloudRecord struct {
	MetadataVersion uint64    `json:"metadataVersion"`
	Provider        string    `json:"provider"`
	Model           string    `json:"model"`
	Source          string    `json:"source"`
	SecretRef       string    `json:"secretRef,omitempty"`
	UpdatedAt       time.Time `json:"updatedAt,omitempty"`
	LastTestAt      time.Time `json:"lastTestAt,omitempty"`
	AvailableModels []string  `json:"availableModels,omitempty"`
}

type cloudUpdate struct {
	MetadataVersion uint64  `json:"metadataVersion"`
	Provider        string  `json:"provider"`
	Model           string  `json:"model"`
	APIKey          *string `json:"apiKey,omitempty"`
}

type cloudManager struct {
	db        *bolt.DB
	dir       string
	host      *DeepSeekProvider
	client    *http.Client
	commit    func(func(*bolt.Tx) error) error
	writeFile func(string, string) (string, error)
}

var cloudModelID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)
var cloudSecretRef = regexp.MustCompile(`^cloud-[0-9a-f]{48}\.key$`)

func (s *Service) initCloudSettings(databaseDir string) error {
	host, ok := s.opts.Provider.(*DeepSeekProvider)
	if !ok || host == nil {
		host = &DeepSeekProvider{Model: "deepseek-flash"}
	}
	dir, client := filepath.Join(databaseDir, "secrets"), host.Client
	if s.opts.Cloud != nil {
		if s.opts.Cloud.SecretDir != "" {
			dir = s.opts.Cloud.SecretDir
		}
		if s.opts.Cloud.Client != nil {
			client = s.opts.Cloud.Client
		}
	}
	s.cloud = &cloudManager{db: s.db, dir: dir, host: host, client: client, commit: s.db.Update, writeFile: writeCloudSecret}
	return s.db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists([]byte("cloud-settings")); err != nil {
			return err
		}
		if tx.Bucket([]byte("cloud-settings")).Get([]byte("active")) != nil {
			return nil // Web settings remain authoritative across restart/bootstrap.
		}
		model := host.Model
		if model == "" {
			model = "deepseek-flash"
		}
		return put(tx, "cloud-settings", "active", cloudRecord{MetadataVersion: 1, Provider: "deepseek", Model: model, Source: "hostfile"})
	})
}

func (m *cloudManager) record() (cloudRecord, error) {
	var record cloudRecord
	err := m.db.View(func(tx *bolt.Tx) error {
		var err error
		record, err = read[cloudRecord](tx, "cloud-settings", "active")
		return err
	})
	return record, err
}

func privateCloudDirectory(dir string, create bool) error {
	if create {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0700 {
		return providerError("provider_not_configured")
	}
	return nil
}

func writeCloudSecret(dir, key string) (string, error) {
	if err := privateCloudDirectory(dir, true); err != nil {
		return "", err
	}
	ref := "cloud-" + newID() + ".key"
	path := filepath.Join(dir, ref)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	completed := false
	defer func() {
		file.Close()
		if !completed {
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(0600); err != nil {
		return "", err
	}
	if _, err := io.WriteString(file, key); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return "", err
	}
	err = directory.Sync()
	directory.Close()
	if err != nil {
		return "", err
	}
	completed = true
	return ref, nil
}

func (m *cloudManager) key(record cloudRecord) (string, error) {
	if record.Source == "hostfile" {
		return m.host.secretKey()
	}
	if record.Source != "managed" || !cloudSecretRef.MatchString(record.SecretRef) || privateCloudDirectory(m.dir, false) != nil {
		return "", providerError("provider_not_configured")
	}
	path := filepath.Join(m.dir, record.SecretRef)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return "", providerError("provider_not_configured")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", providerError("provider_not_configured")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", providerError("provider_not_configured")
	}
	data, err := io.ReadAll(io.LimitReader(file, 8193))
	if err != nil || len(data) > 8192 {
		return "", providerError("provider_not_configured")
	}
	return cleanCloudKey(string(data))
}

func cleanCloudKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 8192 || strings.ContainsAny(key, "\r\n\x00") {
		return "", providerError("provider_not_configured")
	}
	return key, nil
}

func (m *cloudManager) public(record cloudRecord) map[string]any {
	key, err := m.key(record)
	result := map[string]any{"metadataVersion": record.MetadataVersion, "provider": record.Provider, "model": redactText(record.Model, []string{key}), "source": record.Source, "hasKey": err == nil}
	if !record.UpdatedAt.IsZero() {
		result["updatedAt"] = record.UpdatedAt
	}
	if !record.LastTestAt.IsZero() {
		result["lastTestAt"], result["lastTestStatus"] = record.LastTestAt, "passed"
	}
	if len(record.AvailableModels) > 0 {
		result["availableModels"] = record.AvailableModels
	}
	return result
}

func (s *Service) getCloudSettings(w http.ResponseWriter, r *http.Request) error {
	if err := s.admin(r); err != nil {
		return err
	}
	record, err := s.cloud.record()
	if err == nil {
		respond(w, 200, s.cloud.public(record))
	}
	return err
}

func cloudClient(base *http.Client, timeout time.Duration) *http.Client {
	client := http.Client{}
	if base != nil {
		client = *base
	}
	if client.Timeout <= 0 || client.Timeout > timeout {
		client.Timeout = timeout
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client
}

func (m *cloudManager) verify(ctx context.Context, key, model string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, "GET", DeepSeekModelsURL, nil)
	if err != nil {
		return nil, providerError("provider_unavailable")
	}
	request.Header.Set("Authorization", "Bearer "+key)
	response, err := cloudClient(m.client, 10*time.Second).Do(request)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
			return nil, providerError("provider_timeout")
		}
		return nil, providerError("provider_unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return nil, providerError("provider_authentication_failed")
	}
	if response.StatusCode == 429 {
		return nil, providerError("provider_rate_limited")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, providerError("provider_unavailable")
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF || len(body.Data) > 1000 {
		return nil, providerError("provider_invalid_response")
	}
	models, found := make([]string, 0), false
	for _, entry := range body.Data {
		if !cloudModelID.MatchString(entry.ID) || redactText(entry.ID, []string{key}) != entry.ID || contains(models, entry.ID) {
			continue
		}
		models = append(models, entry.ID)
		found = found || entry.ID == model
	}
	if !found {
		return nil, providerError("provider_model_unavailable")
	}
	sort.Strings(models)
	return models, nil
}

func (s *Service) candidateCloud(w http.ResponseWriter, r *http.Request) (cloudUpdate, cloudRecord, string, error) {
	var update cloudUpdate
	if err := s.admin(r); err != nil {
		return update, cloudRecord{}, "", err
	}
	if err := decode(w, r, &update); err != nil {
		return update, cloudRecord{}, "", err
	}
	if update.Provider != "deepseek" || !cloudModelID.MatchString(update.Model) {
		return update, cloudRecord{}, "", fail(400, "invalid_cloud_settings")
	}
	record, err := s.cloud.record()
	if err != nil {
		return update, record, "", err
	}
	if update.MetadataVersion != record.MetadataVersion {
		return update, record, "", fail(409, "cloud_settings_conflict")
	}
	var key string
	if update.APIKey != nil {
		key, err = cleanCloudKey(*update.APIKey)
		if err != nil {
			err = fail(400, "invalid_cloud_settings")
		}
	} else {
		key, err = s.cloud.key(record)
		if err != nil {
			err = cloudVerificationError(err)
		}
	}
	return update, record, key, err
}

func cloudVerificationError(err error) error { return fail(502, providerCode(err)) }

func (s *Service) cloudFailure(r *http.Request, action string, failure error) error {
	if err := s.RecordAudit(r, action+".failed", "cloud-settings", "deepseek", []string{"provider", "model", "apiKey"}); err != nil {
		return err
	}
	return failure
}

func (s *Service) testCloudSettings(w http.ResponseWriter, r *http.Request) error {
	update, _, key, err := s.candidateCloud(w, r)
	if err != nil {
		return err
	}
	models, err := s.cloud.verify(r.Context(), key, update.Model)
	if err != nil {
		return s.cloudFailure(r, "cloud.test", cloudVerificationError(err))
	}
	if err := s.RecordAudit(r, "cloud.test.passed", "cloud-settings", "deepseek", []string{"provider", "model"}); err != nil {
		return err
	}
	respond(w, 200, map[string]any{"ok": true, "metadataVersion": update.MetadataVersion, "provider": "deepseek", "model": update.Model, "availableModels": models, "checkedAt": s.opts.Now().UTC()})
	return nil
}

func (s *Service) saveCloud(r *http.Request, previous, next cloudRecord, newKey *string, fields []string, action string) error {
	newRef := ""
	if newKey != nil {
		var err error
		newRef, err = s.cloud.writeFile(s.cloud.dir, *newKey)
		if err != nil {
			return fail(500, "cloud_secret_write_failed")
		}
		next.Source, next.SecretRef = "managed", newRef
	}
	err := s.cloud.commit(func(tx *bolt.Tx) error {
		if err := s.admin(r); err != nil {
			return err
		}
		current, err := read[cloudRecord](tx, "cloud-settings", "active")
		if err != nil {
			return err
		}
		if current.MetadataVersion != previous.MetadataVersion {
			return fail(409, "cloud_settings_conflict")
		}
		next.MetadataVersion = current.MetadataVersion + 1
		next.UpdatedAt, next.LastTestAt = s.opts.Now().UTC(), s.opts.Now().UTC()
		if err := put(tx, "cloud-settings", "active", next); err != nil {
			return err
		}
		return s.auditTx(tx, r, action, "cloud-settings", "deepseek", fields)
	})
	if err != nil && newRef != "" {
		_ = os.Remove(filepath.Join(s.cloud.dir, newRef))
	}
	return err
}

func (s *Service) updateCloudSettings(w http.ResponseWriter, r *http.Request) error {
	update, record, key, err := s.candidateCloud(w, r)
	if err != nil {
		return err
	}
	models, err := s.cloud.verify(r.Context(), key, update.Model)
	if err != nil {
		return s.cloudFailure(r, "cloud.update", cloudVerificationError(err))
	}
	next := record
	next.Provider, next.Model, next.AvailableModels = "deepseek", update.Model, models
	var newKey *string
	fields := []string{"provider", "model"}
	if update.APIKey != nil {
		newKey, fields = &key, append(fields, "source", "apiKey")
	}
	if err := s.saveCloud(r, record, next, newKey, fields, "cloud.update"); err != nil {
		return s.cloudFailure(r, "cloud.update", err)
	}
	next, err = s.cloud.record()
	if err == nil {
		respond(w, 200, s.cloud.public(next))
	}
	return err
}

func (s *Service) restoreCloudHostfile(w http.ResponseWriter, r *http.Request) error {
	if err := s.admin(r); err != nil {
		return err
	}
	var update struct {
		MetadataVersion uint64 `json:"metadataVersion"`
	}
	if err := decode(w, r, &update); err != nil {
		return err
	}
	record, err := s.cloud.record()
	if err != nil {
		return err
	}
	if record.MetadataVersion != update.MetadataVersion {
		return fail(409, "cloud_settings_conflict")
	}
	key, err := s.cloud.host.secretKey()
	if err != nil {
		return s.cloudFailure(r, "cloud.restore_hostfile", cloudVerificationError(err))
	}
	model := s.cloud.host.Model
	if model == "" {
		model = "deepseek-flash"
	}
	models, err := s.cloud.verify(r.Context(), key, model)
	if err != nil {
		return s.cloudFailure(r, "cloud.restore_hostfile", cloudVerificationError(err))
	}
	next := cloudRecord{Provider: "deepseek", Model: model, Source: "hostfile", AvailableModels: models}
	if err := s.saveCloud(r, record, next, nil, []string{"source", "model"}, "cloud.restore_hostfile"); err != nil {
		return s.cloudFailure(r, "cloud.restore_hostfile", err)
	}
	next, err = s.cloud.record()
	if err == nil {
		respond(w, 200, s.cloud.public(next))
	}
	return err
}
