package edgestack

import (
	"net/http"

	bolt "go.etcd.io/bbolt"
)

func (s *Service) deliveryCatalog(w http.ResponseWriter, r *http.Request) error {
	principal, err := s.principal(r)
	if err != nil {
		return err
	}
	type entry struct {
		PackageID      string   `json:"packageId"`
		Name           string   `json:"name"`
		Version        string   `json:"version"`
		RecipeID       string   `json:"recipeId"`
		Image          string   `json:"image"`
		Architecture   string   `json:"architecture"`
		RuntimeBackend string   `json:"runtimeBackend"`
		ArtifactID     string   `json:"artifactId,omitempty"`
		DeviceIDs      []string `json:"deviceIds"`
	}
	result := []entry{}
	err = s.db.View(func(tx *bolt.Tx) error {
		devices, e := list[Device](tx, "devices")
		if e != nil {
			return e
		}
		models, e := list[ModelPackage](tx, "models")
		if e != nil {
			return e
		}
		tasks, e := list[Task](tx, "tasks")
		if e != nil {
			return e
		}
		recipes, e := list[DeliveryRecipe](tx, "delivery-recipes")
		if e != nil {
			return e
		}
		for _, model := range models {
			if !model.Published {
				continue
			}
			candidates := map[string]*entry{}
			appendDevice := func(recipeID string, device Device) {
				item := candidates[recipeID]
				if item == nil {
					item = &entry{PackageID: model.ID, Name: model.Name, Version: model.Version, RecipeID: recipeID, Image: model.Image, Architecture: device.Architecture, RuntimeBackend: model.RuntimeBackend, ArtifactID: model.ArtifactID, DeviceIDs: []string{}}
					candidates[recipeID] = item
				}
				if !contains(item.DeviceIDs, device.ID) {
					item.DeviceIDs = append(item.DeviceIDs, device.ID)
				}
			}
			for _, device := range devices {
				if !s.canDevice(r, principal, device) || (r.URL.Query().Get("deviceId") != "" && r.URL.Query().Get("deviceId") != device.ID) {
					continue
				}
				for _, task := range tasks {
					if task.DeviceID == device.ID && s.canTask(r, principal, tx, task) && compatible(task, device, model) && s.policyAllows(tx, task, device, model, principal) {
						appendDevice("", device)
					}
				}
				for _, recipe := range recipes {
					if !recipe.Enabled {
						continue
					}
					task := Task{Kind: recipe.TaskKind, RuntimeBackend: recipe.RuntimeBackend, TemplateID: recipe.TemplateID, Capabilities: []string{"replace-model"}, Models: []ModelBinding{{Slot: recipe.Slot}}}
					for key := range model.Environment {
						task.ModelEnvKeys = append(task.ModelEnvKeys, key)
					}
					if compatible(task, device, model) && s.policyAllows(tx, task, device, model, principal) {
						appendDevice(recipe.ID, device)
					}
				}
			}
			for _, item := range candidates {
				result = append(result, *item)
			}
		}
		return nil
	})
	if err == nil {
		respond(w, 200, map[string]any{"items": result})
	}
	return err
}

func (s *Service) deliveryArtifactReadable(tx *bolt.Tx, r *http.Request, artifact DeliveryArtifact) bool {
	principal, err := s.principal(r)
	if err != nil {
		return false
	}
	job, err := read[deliveryJobRecord](tx, "delivery-jobs", artifact.JobID)
	if err != nil {
		return false
	}
	model, err := read[ModelPackage](tx, "models", job.ValidationPackageID)
	if err != nil || !model.Published || model.ArtifactID != artifact.ID {
		return false
	}
	devices, err := list[Device](tx, "devices")
	if err != nil {
		return false
	}
	tasks, err := list[Task](tx, "tasks")
	if err != nil {
		return false
	}
	recipes, err := list[DeliveryRecipe](tx, "delivery-recipes")
	if err != nil {
		return false
	}
	for _, device := range devices {
		if !s.canDevice(r, principal, device) {
			continue
		}
		for _, task := range tasks {
			if task.DeviceID == device.ID && s.canTask(r, principal, tx, task) && compatible(task, device, model) && s.policyAllows(tx, task, device, model, principal) {
				return true
			}
		}
		for _, recipe := range recipes {
			if !recipe.Enabled {
				continue
			}
			task := Task{Kind: recipe.TaskKind, RuntimeBackend: recipe.RuntimeBackend, TemplateID: recipe.TemplateID, Capabilities: []string{"replace-model"}, Models: []ModelBinding{{Slot: recipe.Slot}}}
			for key := range model.Environment {
				task.ModelEnvKeys = append(task.ModelEnvKeys, key)
			}
			if compatible(task, device, model) && s.policyAllows(tx, task, device, model, principal) {
				return true
			}
		}
	}
	return false
}

func (s *Service) deliveryArtifactPolicyAccess(tx *bolt.Tx, r *http.Request, job deliveryJobRecord) bool {
	principal, err := s.principal(r)
	if err != nil {
		return false
	}
	policy, err := read[DeliveryPolicy](tx, "delivery-policies", job.PolicyID)
	return err == nil && policy.Enabled && allows(principal, policy.ACL)
}
