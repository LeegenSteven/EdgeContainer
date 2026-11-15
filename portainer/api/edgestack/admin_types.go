package edgestack

import "time"

type AuditEntry struct {
	ID           string    `json:"id"`
	ActorUserID  int       `json:"actorUserId"`
	Action       string    `json:"action"`
	ResourceType string    `json:"resourceType"`
	ResourceID   string    `json:"resourceId"`
	DeviceID     string    `json:"deviceId,omitempty"`
	TaskID       string    `json:"taskId,omitempty"`
	OccurredAt   time.Time `json:"occurredAt"`
	Result       string    `json:"result"`
	Fields       []string  `json:"fields"`
}
type PublicationScope struct {
	TaskKind       string `json:"taskKind"`
	Architecture   string `json:"architecture"`
	RuntimeBackend string `json:"runtimeBackend,omitempty"`
	TemplateID     string `json:"templateId,omitempty"`
}
type ModelPolicy struct {
	MetadataVersion  int                         `json:"metadataVersion"`
	ValidationStatus string                      `json:"validationStatus"`
	LegacyScoped     bool                        `json:"legacyScoped"`
	AllowedTaskIDs   []string                    `json:"allowedTaskIds"`
	LegacyTaskScopes map[string]PublicationScope `json:"legacyTaskScopes,omitempty"`
	ValidatedScopes  []PublicationScope          `json:"validatedScopes"`
	LastValidationID string                      `json:"lastValidationId,omitempty"`
}
type ModelValidation struct {
	ID          string           `json:"id"`
	OperationID string           `json:"operationId"`
	PackageID   string           `json:"packageId"`
	TaskID      string           `json:"taskId"`
	DeviceID    string           `json:"deviceId"`
	ActorUserID int              `json:"actorUserId"`
	Status      string           `json:"status"`
	CreatedAt   time.Time        `json:"createdAt"`
	UpdatedAt   time.Time        `json:"updatedAt"`
	ObservedAt  time.Time        `json:"observedAt,omitempty"`
	SourceID    string           `json:"sourceId,omitempty"`
	ModelID     string           `json:"modelId,omitempty"`
	ModelHash   string           `json:"modelHash,omitempty"`
	ErrorCode   string           `json:"errorCode,omitempty"`
	Scope       PublicationScope `json:"scope"`
}
type ImportChange struct {
	Kind            string   `json:"kind"`
	ID              string   `json:"id"`
	Action          string   `json:"action"`
	Fields          []string `json:"fields"`
	MetadataVersion int      `json:"metadataVersion"`
}
type importRecord struct {
	ID        string          `json:"id"`
	UserID    int             `json:"userId"`
	ExpiresAt time.Time       `json:"expiresAt"`
	Applied   bool            `json:"applied"`
	Config    BootstrapConfig `json:"config"`
	Changes   []ImportChange  `json:"changes"`
}
