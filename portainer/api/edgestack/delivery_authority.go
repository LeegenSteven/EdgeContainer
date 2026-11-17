package edgestack

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"

	bolt "go.etcd.io/bbolt"
)

func (s *Service) DeliveryAuthenticateWorker(r *http.Request) error { return s.deliveryWorker(r) }
func (s *Service) DeliveryControlEpoch() string                     { return s.deliveryEpoch }
func (s *Service) DeliveryValidateClaim(jobID string, claim DeliveryClaim, deviceID string) (DeliveryJob, error) {
	var job deliveryJobRecord
	err := s.db.View(func(tx *bolt.Tx) error {
		var e error
		job, e = s.claimDelivery(tx, jobID, claim)
		if e != nil {
			return e
		}
		if deviceID != job.ValidationDeviceID {
			return fail(403, "delivery_execution_target_mismatch")
		}
		return s.deliveryJobAuthorized(tx, nil, job)
	})
	return job.DeliveryJob, err
}
func (s *Service) DeliveryEndpoint(deviceID string) (int, error) {
	var device Device
	err := s.db.View(func(tx *bolt.Tx) error {
		var e error
		device, e = read[Device](tx, "devices", deviceID)
		if e == nil && device.Archived {
			return fail(404, "not_found")
		}
		return e
	})
	return device.EndpointID, err
}
func (s *Service) DeliveryDeviceManaged(endpoint int) bool {
	managed := false
	_ = s.db.View(func(tx *bolt.Tx) error {
		recipes, err := list[DeliveryRecipe](tx, "delivery-recipes")
		if err != nil {
			return err
		}
		for _, recipe := range recipes {
			if recipe.Enabled {
				if task, e := read[Task](tx, "tasks", recipe.ValidationTaskID); e == nil && task.Target.EndpointID == endpoint {
					managed = true
				}
			}
		}
		tasks, err := list[Task](tx, "tasks")
		if err != nil {
			return err
		}
		for _, task := range tasks {
			if task.Archived || task.Target.EndpointID != endpoint {
				continue
			}
			for _, binding := range task.Models {
				if model, e := read[ModelPackage](tx, "models", binding.PackageID); e == nil && model.ArtifactID != "" {
					managed = true
				}
			}
		}
		return nil
	})
	return managed
}

type deliveryObserverBinding struct {
	OperationID  string `json:"operationId"`
	TaskID       string `json:"taskId"`
	SourceID     string `json:"sourceId"`
	CredentialID string `json:"credentialId"`
}

func (s *Service) initDeliveryObserverKey() error {
	path := filepath.Join(filepath.Dir(s.db.Path()), "delivery-observer-key")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		_, err = file.Write([]byte(newID() + newID()))
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
	} else if !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return fail(503, "delivery_observer_key_invalid")
	}
	key, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(key) < 64 {
		return fail(503, "delivery_observer_key_invalid")
	}
	s.deliveryObserverKey = key
	return nil
}
func (s *Service) deliveryObserverToken(binding deliveryObserverBinding) string {
	hash := hmac.New(sha256.New, s.deliveryObserverKey)
	_, _ = hash.Write([]byte(binding.OperationID + "\x00" + binding.TaskID + "\x00" + binding.SourceID))
	return "es_ingest_" + hex.EncodeToString(hash.Sum(nil))
}
