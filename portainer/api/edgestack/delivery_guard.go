package edgestack

import (
	"encoding/json"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

func (s *Service) deliveryArtifactAllows(tx *bolt.Tx, task Task, device Device, model ModelPackage, principal Principal) bool {
	if model.ArtifactID == "" {
		return true
	}
	artifact, err := read[DeliveryArtifact](tx, "delivery-artifacts", model.ArtifactID)
	if err != nil || artifact.Image != model.Image || artifact.ModelHash != model.ModelHash || artifact.ModelID != model.ModelID || artifact.Digest != deliveryDigest(artifact) {
		return false
	}
	job, err := read[deliveryJobRecord](tx, "delivery-jobs", artifact.JobID)
	if err != nil || (job.Status != "published" && job.Status != "artifact_ready") || job.ArtifactID != artifact.ID || job.Recipe.Architecture != device.Architecture || job.Recipe.RuntimeBackend != task.RuntimeBackend || job.Recipe.TemplateID != task.TemplateID || !contains(job.Policy.DeviceIDs, device.ID) {
		return false
	}
	policy, err := read[DeliveryPolicy](tx, "delivery-policies", job.PolicyID)
	if err != nil || !allows(principal, policy.ACL) || !policy.Enabled || !contains(policy.DeviceIDs, device.ID) || !contains(policy.RecipeIDs, job.RecipeID) {
		return false
	}
	recipe, err := read[DeliveryRecipe](tx, "delivery-recipes", job.RecipeID)
	if err != nil || !recipe.Enabled || deliveryDigest(recipe) != job.RecipeDigest {
		return false
	}
	return true
}
func (s *Service) restoreDelivery(tx *bolt.Tx) error {
	for _, bucket := range []string{"delivery-jobs", "delivery-plans", "delivery-rollouts"} {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			continue
		}
		cursor := b.Cursor()
		for key, value := cursor.First(); key != nil; key, value = cursor.Next() {
			// JSON map updates preserve future additive fields in old restore images.
			var item map[string]any
			if err := json.Unmarshal(value, &item); err != nil {
				return err
			}
			status, _ := item["status"].(string)
			if status == "published" || status == "succeeded" || status == "failed" || status == "rolled_back" {
				continue
			}
			item["status"] = "needs_review"
			item["errorCode"] = "restored_delivery_requires_review"
			delete(item, "secretHash")
			item["updatedAt"] = s.opts.Now().UTC().Format(time.RFC3339Nano)
			if err := put(tx, bucket, string(key), item); err != nil {
				return err
			}
		}
	}
	// Restored artifacts retain provenance, while policy gates require renewed
	// administrator resolution before another automatic publication.
	policies, err := list[DeliveryPolicy](tx, "delivery-policies")
	if err != nil {
		return err
	}
	for _, policy := range policies {
		policy.Enabled = false
		policy.MetadataVersion++
		if err = put(tx, "delivery-policies", policy.ID, policy); err != nil {
			return err
		}
	}
	if err := s.restoreDeliveryExecution(tx); err != nil {
		return err
	}
	outbox := tx.Bucket([]byte("delivery-outbox"))
	if outbox != nil {
		var keys [][]byte
		_ = outbox.ForEach(func(key, _ []byte) error { keys = append(keys, append([]byte{}, key...)); return nil })
		for _, key := range keys {
			if err = outbox.Delete(key); err != nil {
				return err
			}
		}
	}
	return nil
}
func deliveryTerminal(status string) bool {
	return status == "published" || status == "artifact_ready" || status == "failed" || status == "needs_review" || strings.HasPrefix(status, "cancel")
}
