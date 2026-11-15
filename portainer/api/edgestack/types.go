// Package edgestack implements the business API without changing Portainer's
// administration API or granting business users generic container access.
package edgestack

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

const Retention = 7 * 24 * time.Hour

type Principal struct {
	UserID  int
	TeamIDs []int
	Admin   bool
}

type ACL struct {
	UserIDs []int `json:"userIds"`
	TeamIDs []int `json:"teamIds"`
}

type Device struct {
	MetadataVersion int    `json:"metadataVersion"`
	Description     string `json:"description,omitempty"`
	Archived        bool   `json:"archived"`
	ID              string `json:"id"`
	Name            string `json:"name"`
	EndpointID      int    `json:"endpointId"`
	ACL
	Architecture string `json:"architecture,omitempty"`
}

type Target struct {
	EndpointID int    `json:"endpointId"`
	StackID    int    `json:"stackId"`
	StackName  string `json:"stackName,omitempty"`
	ServiceID  string `json:"serviceId"`
}

type ModelBinding struct {
	Slot              string    `json:"slot"`
	PackageID         string    `json:"packageId"`
	Name              string    `json:"name"`
	Version           string    `json:"version"`
	ObservedPackageID string    `json:"observedPackageId,omitempty"`
	ObservedModelID   string    `json:"observedModelId,omitempty"`
	ObservedModelHash string    `json:"observedModelHash,omitempty"`
	ObservedAt        time.Time `json:"observedAt,omitempty"`
	State             string    `json:"state"`
}

type Task struct {
	MetadataVersion int    `json:"metadataVersion"`
	Description     string `json:"description,omitempty"`
	Archived        bool   `json:"archived"`
	TestTask        bool   `json:"testTask"`
	RuntimeBackend  string `json:"runtimeBackend,omitempty"`
	TemplateID      string `json:"templateId,omitempty"`
	ID              string `json:"id"`
	DeviceID        string `json:"deviceId"`
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	State           string `json:"state"`
	Revision        int    `json:"revision"`
	Target          Target `json:"target"`
	ACL
	Models        []ModelBinding    `json:"models"`
	DataLocations []json.RawMessage `json:"dataLocations"`
	Capabilities  []string          `json:"capabilities"`
	// Only these existing environment variable names may be replaced. The
	// immutable package supplies their values; clients cannot supply overrides.
	ModelEnvKeys   []string `json:"modelEnvKeys,omitempty"`
	VoiceOrigin    string   `json:"voiceOrigin,omitempty"`
	VoiceTokenFile string   `json:"voiceTokenFile,omitempty"`
}

type ModelPackage struct {
	RuntimeBackend  string            `json:"runtimeBackend,omitempty"`
	TemplateIDs     []string          `json:"templateIds,omitempty"`
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Version         string            `json:"version"`
	Kind            string            `json:"kind"`
	Slot            string            `json:"slot"`
	TaskKinds       []string          `json:"taskKinds"`
	Architectures   []string          `json:"architectures"`
	Published       bool              `json:"published"`
	Immutable       bool              `json:"immutable"`
	Image           string            `json:"image"`
	ModelID         string            `json:"modelId"`
	ModelHash       string            `json:"modelHash"`
	Environment     map[string]string `json:"environment"`
	RequiresRestart bool              `json:"requiresRestart"`
}

type Source struct {
	MetadataVersion int             `json:"metadataVersion"`
	Description     string          `json:"description,omitempty"`
	Archived        bool            `json:"archived"`
	ID              string          `json:"id"`
	DeviceID        string          `json:"deviceId"`
	TaskID          string          `json:"taskId"`
	Name            string          `json:"name"`
	Type            string          `json:"type"`
	Enabled         bool            `json:"enabled"`
	Status          string          `json:"status"`
	LastSeenAt      time.Time       `json:"lastSeenAt,omitempty"`
	LastEventAt     time.Time       `json:"lastEventAt,omitempty"`
	Checkpoint      json.RawMessage `json:"checkpoint,omitempty"`
	Coverage        json.RawMessage `json:"coverage,omitempty"`
	ErrorCode       string          `json:"errorCode,omitempty"`
}

type Event struct {
	SchemaVersion  int             `json:"schemaVersion"`
	EventID        string          `json:"eventId"`
	DeviceID       string          `json:"deviceId"`
	SourceID       string          `json:"sourceId"`
	TaskID         string          `json:"taskId"`
	Type           string          `json:"type"`
	OccurredAt     time.Time       `json:"occurredAt"`
	ReceivedAt     time.Time       `json:"receivedAt"`
	SessionID      string          `json:"sessionId"`
	Sequence       int64           `json:"sequence"`
	Text           string          `json:"text"`
	ReplyToEventID string          `json:"replyToEventId,omitempty"`
	Provenance     json.RawMessage `json:"provenance,omitempty"`
	Phase          string          `json:"phase,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
}

type Coverage struct {
	From             time.Time `json:"from"`
	To               time.Time `json:"to"`
	RetentionDays    int       `json:"retentionDays"`
	EventCount       int       `json:"eventCount"`
	SourceCount      int       `json:"sourceCount"`
	AvailableSources int       `json:"availableSources"`
	Gaps             []string  `json:"gaps"`
	Complete         bool      `json:"complete"`
}

type Credential struct {
	CreatedAt        time.Time `json:"createdAt,omitempty"`
	CreatedByUserID  int       `json:"createdByUserId,omitempty"`
	LastUsedAt       time.Time `json:"lastUsedAt,omitempty"`
	LastSuccessfulAt time.Time `json:"lastSuccessfulAt,omitempty"`
	RevokedAt        time.Time `json:"revokedAt,omitempty"`
	RotatedFromID    string    `json:"rotatedFromId,omitempty"`
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	DeviceIDs        []string  `json:"deviceIds"`
	SourceIDs        []string  `json:"sourceIds"`
	TaskIDs          []string  `json:"taskIds"`
	Capabilities     []string  `json:"capabilities"`
	Hash             string    `json:"-"`
	Revoked          bool      `json:"revoked"`
}

type Preview struct {
	Purpose           string    `json:"purpose,omitempty"`
	ID                string    `json:"id"`
	UserID            int       `json:"-"`
	TaskID            string    `json:"taskId"`
	PackageID         string    `json:"packageId"`
	PreviousPackageID string    `json:"previousPackageId,omitempty"`
	Slot              string    `json:"slot"`
	Revision          int       `json:"revision"`
	Target            Target    `json:"target"`
	TargetVersion     string    `json:"targetVersion"`
	RequiresRestart   bool      `json:"requiresRestart"`
	Effects           []string  `json:"effects"`
	ExpiresAt         time.Time `json:"expiresAt"`
	OperationID       string    `json:"operationId,omitempty"`
}

type Operation struct {
	ActorUserID       int       `json:"actorUserId,omitempty"`
	Target            Target    `json:"target"`
	Purpose           string    `json:"purpose,omitempty"`
	ValidationID      string    `json:"validationId,omitempty"`
	ID                string    `json:"id"`
	TaskID            string    `json:"taskId"`
	DeviceID          string    `json:"deviceId"`
	Status            string    `json:"status"`
	PackageID         string    `json:"packageId"`
	PreviousPackageID string    `json:"previousPackageId,omitempty"`
	Slot              string    `json:"slot"`
	Revision          int       `json:"revision"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
	ErrorCode         string    `json:"errorCode,omitempty"`
}

type Evidence struct {
	Ref        string          `json:"ref"`
	EventID    string          `json:"eventId"`
	Text       string          `json:"text"`
	OccurredAt time.Time       `json:"occurredAt"`
	Type       string          `json:"type"`
	Provenance json.RawMessage `json:"provenance,omitempty"`
	Phase      string          `json:"phase,omitempty"`
}

type Topic struct {
	Text      string   `json:"text"`
	EventRefs []string `json:"eventRefs"`
}

type SummaryRequest struct {
	TimeZone string    `json:"timeZone,omitempty"`
	DeviceID string    `json:"deviceId"`
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Question string    `json:"question"`
	Coverage Coverage  `json:"-"`
}

type Summary struct {
	UsedTimeZone  string     `json:"usedTimeZone"`
	ID            string     `json:"id"`
	Status        string     `json:"status"`
	Title         string     `json:"title"`
	Summary       string     `json:"summary"`
	Topics        []Topic    `json:"topics"`
	Answer        string     `json:"answer"`
	Uncertainties []string   `json:"uncertainties"`
	Coverage      Coverage   `json:"coverage"`
	Evidence      []Evidence `json:"evidence"`
	ErrorCode     string     `json:"errorCode,omitempty"`
}

type Provider interface {
	Summarize(context.Context, SummaryRequest, []Evidence) (Summary, error)
}

// Dispatcher validates and updates an independent, administrator-registered
// Edge Stack through the same datastore/version/status/file mechanism as the
// existing Edge Stack updater. The expected version prevents stale previews.
type Dispatcher interface {
	Preview(context.Context, Task, ModelPackage) (string, error)
	Apply(context.Context, Task, ModelPackage, string) error
}

// MountProvider exposes only checks of explicitly registered container paths.
// Raw Docker inspection data, host sources and unrelated mounts never cross
// this interface or enter the public business projection.
type MountRequest struct {
	ContainerPath    string
	ExpectedReadOnly *bool
	VolumeName       string
}
type MountVerification struct {
	Verified   bool
	ObservedAt time.Time
	Reason     string
}
type MountProvider interface {
	Verify(context.Context, Task, []MountRequest) []MountVerification
}
type RuntimeObservation struct {
	State      string
	ObservedAt time.Time
}
type RuntimeProvider interface {
	ObserveRuntime(context.Context, Task) RuntimeObservation
}

type Options struct {
	Cloud                *CloudOptions
	ValidateACL          func(ACL) error
	InspectAccess        func(int, int) (Principal, bool, error)
	BackgroundWriteLease func() (func(), error)
	Authenticate         func(*http.Request) (Principal, error)
	EndpointAccess       func(*http.Request, int) bool
	Authenticated        func(http.Handler) http.Handler
	AdminAccess          func(http.Handler) http.Handler
	Dispatcher           Dispatcher
	ValidateTask         func(context.Context, Task) error
	VerifyModelRuntime   func(context.Context, Task, ModelPackage) error
	Provider             Provider
	Now                  func() time.Time
	DeviceStatus         func(*http.Request, int) (bool, time.Time)
	VoiceTestHTML        []byte
	MountProvider        MountProvider
}
