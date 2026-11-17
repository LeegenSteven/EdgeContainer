package edgestack

import (
	"context"
	"encoding/json"
	"time"
)

// DeliveryProvisioner deploys an administrator-installed task template. Apply
// must reconcile the same operationID after a lost reply instead of redeploying.
type DeliveryProvisioner interface {
	Preview(context.Context, DeliveryProvisionSpec) (DeliveryProvisionPreview, error)
	Apply(context.Context, DeliveryProvisionSpec, DeliveryProvisionPreview, string) (Task, error)
}
type DeliveryProvisionSpec struct {
	ObserverSourceID string         `json:"-"`
	ObserverToken    string         `json:"-"`
	Device           Device         `json:"device"`
	Package          ModelPackage   `json:"package"`
	Recipe           DeliveryRecipe `json:"recipe"`
	TaskID           string         `json:"taskId"`
	Name             string         `json:"name"`
	ACL              ACL            `json:"acl"`
}
type DeliveryProvisionPreview struct {
	TargetVersion string          `json:"targetVersion"`
	Effects       []string        `json:"effects"`
	ProviderData  json.RawMessage `json:"providerData,omitempty"`
}
type DeliveryGate struct {
	MaxLatencyMs   float64 `json:"maxLatencyMs"`
	MaxMemoryBytes int64   `json:"maxMemoryBytes"`
	MaxErrorRate   float64 `json:"maxErrorRate"`
	MinQuality     float64 `json:"minQuality"`
}
type DeliveryRecipe struct {
	ID                   string            `json:"id"`
	MetadataVersion      int               `json:"metadataVersion"`
	Version              string            `json:"version"`
	Enabled              bool              `json:"enabled"`
	Engine               string            `json:"engine"`
	TaskKind             string            `json:"taskKind"`
	Architecture         string            `json:"architecture"`
	RuntimeBackend       string            `json:"runtimeBackend"`
	TemplateID           string            `json:"templateId"`
	Slot                 string            `json:"slot"`
	BaseImage            string            `json:"baseImage"`
	RuntimeProfileDigest string            `json:"runtimeProfileDigest"`
	SourcePrefixes       []string          `json:"sourcePrefixes"`
	ValidationTaskID     string            `json:"validationTaskId"`
	ValidationSourceID   string            `json:"validationSourceId"`
	ModelEnvironment     map[string]string `json:"modelEnvironment"`
	Validation           DeliveryGate      `json:"validation"`
}
type DeliveryPolicy struct {
	ID                      string   `json:"id"`
	MetadataVersion         int      `json:"metadataVersion"`
	Enabled                 bool     `json:"enabled"`
	AllowStopValidationTask bool     `json:"allowStopValidationTask"`
	AutoPublish             bool     `json:"autoPublish"`
	RecipeIDs               []string `json:"recipeIds"`
	DeviceIDs               []string `json:"deviceIds"`
	ACL
}
type DeliverySource struct {
	URI     string `json:"uri"`
	SHA256  string `json:"sha256"`
	ModelID string `json:"modelId"`
	Format  string `json:"format"`
}
type DeliveryMessage struct {
	ID          string    `json:"id"`
	ActorUserID int       `json:"actorUserId"`
	Message     string    `json:"message"`
	CreatedAt   time.Time `json:"createdAt"`
}
type ModelRequest struct {
	ID          string            `json:"id"`
	ActorUserID int               `json:"actorUserId"`
	DeviceID    string            `json:"deviceId"`
	TaskID      string            `json:"taskId,omitempty"`
	ModelName   string            `json:"modelName"`
	Status      string            `json:"status"`
	Messages    []DeliveryMessage `json:"messages"`
	RecipeID    string            `json:"recipeId,omitempty"`
	JobID       string            `json:"jobId,omitempty"`
	PackageID   string            `json:"packageId,omitempty"`
	ErrorCode   string            `json:"errorCode,omitempty"`
	CreatedAt   time.Time         `json:"createdAt"`
	UpdatedAt   time.Time         `json:"updatedAt"`
}
type DeliveryJob struct {
	ID                    string         `json:"id"`
	RequestID             string         `json:"requestId"`
	Phase                 string         `json:"phase"`
	Kind                  string         `json:"kind"`
	Status                string         `json:"status"`
	RecipeID              string         `json:"recipeId"`
	Recipe                DeliveryRecipe `json:"recipe"`
	RecipeDigest          string         `json:"recipeDigest"`
	Source                DeliverySource `json:"source"`
	DeviceID              string         `json:"deviceId"`
	ValidationTaskID      string         `json:"validationTaskId"`
	ValidationDeviceID    string         `json:"validationDeviceId"`
	PolicyID              string         `json:"policyId"`
	PolicyVersion         int            `json:"policyVersion"`
	Policy                DeliveryPolicy `json:"policy"`
	ActorUserID           int            `json:"actorUserId"`
	ResolverUserID        int            `json:"resolverUserId"`
	Attempt               int            `json:"attempt"`
	WorkerID              string         `json:"workerId,omitempty"`
	ClaimEpoch            int            `json:"claimEpoch"`
	ControlEpoch          string         `json:"controlEpoch"`
	ClaimHash             string         `json:"-"`
	LeaseExpiresAt        time.Time      `json:"leaseExpiresAt"`
	ValidationOperationID string         `json:"validationOperationId,omitempty"`
	ValidationPackageID   string         `json:"validationPackageId,omitempty"`
	ArtifactID            string         `json:"artifactId,omitempty"`
	ErrorCode             string         `json:"errorCode,omitempty"`
	CreatedAt             time.Time      `json:"createdAt"`
	UpdatedAt             time.Time      `json:"updatedAt"`
}

// Claim secrets are persisted separately because public job projections must
// never serialize the bearer proof.
type deliveryJobRecord struct {
	DeliveryJob
	SecretHash   string `json:"secretHash"`
	ResultID     string `json:"resultId,omitempty"`
	ResultDigest string `json:"resultDigest,omitempty"`
}
type DeliveryBlob struct {
	URI    string `json:"uri"`
	SHA256 string `json:"sha256"`
}
type DeliveryFile struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"sizeBytes"`
}
type DeliveryArtifact struct {
	ID                   string         `json:"id"`
	Digest               string         `json:"digest"`
	JobID                string         `json:"jobId,omitempty"`
	ModelID              string         `json:"modelId"`
	ModelHash            string         `json:"modelHash"`
	Image                string         `json:"image"`
	Platform             string         `json:"platform"`
	RecipeDigest         string         `json:"recipeDigest"`
	RuntimeProfileDigest string         `json:"runtimeProfileDigest"`
	ReleaseDigest        string         `json:"releaseDigest"`
	ReportDigest         string         `json:"reportDigest"`
	RegistryVerified     bool           `json:"registryVerified"`
	OfflineBundle        DeliveryBlob   `json:"offlineBundle"`
	Files                []DeliveryFile `json:"files"`
}
type DeliveryReport struct {
	ID                   string    `json:"id"`
	Digest               string    `json:"digest"`
	JobID                string    `json:"jobId"`
	RecipeDigest         string    `json:"recipeDigest"`
	ReleaseDigest        string    `json:"releaseDigest"`
	Image                string    `json:"image"`
	ModelHash            string    `json:"modelHash"`
	RuntimeProfileDigest string    `json:"runtimeProfileDigest"`
	DeviceID             string    `json:"deviceId"`
	TaskID               string    `json:"taskId"`
	Status               string    `json:"status"`
	NativeVerified       bool      `json:"nativeVerified"`
	ContainerVerified    bool      `json:"containerVerified"`
	Simulated            bool      `json:"simulated"`
	LatencyMs            float64   `json:"latencyMs"`
	MemoryBytes          int64     `json:"memoryBytes"`
	ErrorRate            float64   `json:"errorRate"`
	Quality              float64   `json:"quality"`
	ObservedAt           time.Time `json:"observedAt"`
}
type DeliveryClaim struct {
	WorkerID     string `json:"workerId"`
	ClaimToken   string `json:"claimToken"`
	ClaimEpoch   int    `json:"claimEpoch"`
	ControlEpoch string `json:"controlEpoch"`
}
type DeliveryTarget struct {
	DeviceID string `json:"deviceId"`
	TaskID   string `json:"taskId,omitempty"`
	TaskName string `json:"taskName,omitempty"`
}
type DeliveryPlanTarget struct {
	DeliveryTarget
	NewTask   bool                      `json:"newTask"`
	Preview   Preview                   `json:"preview,omitempty"`
	Provision *DeliveryProvisionPreview `json:"provision,omitempty"`
}
type DeliveryRolloutTarget struct {
	DeliveryPlanTarget
	Status            string    `json:"status"`
	OperationID       string    `json:"operationId,omitempty"`
	PreviousPackageID string    `json:"previousPackageId,omitempty"`
	HealthChecks      int       `json:"healthChecks"`
	ObservingAt       time.Time `json:"observingAt"`
	LastHealthAt      time.Time `json:"lastHealthAt"`
}
type DeliveryProvisionRollback interface {
	Rollback(context.Context, DeliveryProvisionSpec, string) error
}
type deliveryPlan struct {
	Targets     []DeliveryPlanTarget      `json:"targets"`
	ID          string                    `json:"id"`
	ActorUserID int                       `json:"actorUserId"`
	DeviceID    string                    `json:"deviceId"`
	TaskID      string                    `json:"taskId"`
	PackageID   string                    `json:"packageId"`
	TaskName    string                    `json:"taskName,omitempty"`
	Recipe      DeliveryRecipe            `json:"recipe,omitempty"`
	Preview     Preview                   `json:"preview,omitempty"`
	Provision   *DeliveryProvisionPreview `json:"provision,omitempty"`
	Status      string                    `json:"status"`
	OperationID string                    `json:"operationId,omitempty"`
	ExpiresAt   time.Time                 `json:"expiresAt"`
}
type DeliveryRollout struct {
	Targets       []DeliveryRolloutTarget `json:"targets"`
	CurrentTarget int                     `json:"currentTarget"`
	RollingBack   bool                    `json:"rollingBack"`
	ID            string                  `json:"id"`
	PlanID        string                  `json:"planId"`
	ActorUserID   int                     `json:"actorUserId"`
	DeviceID      string                  `json:"deviceId"`
	TaskID        string                  `json:"taskId"`
	PackageID     string                  `json:"packageId"`
	OperationID   string                  `json:"operationId"`
	Status        string                  `json:"status"`
	ErrorCode     string                  `json:"errorCode,omitempty"`
	UpdatedAt     time.Time               `json:"updatedAt"`
}
