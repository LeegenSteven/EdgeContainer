package edgestack

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/mux"
	bolt "go.etcd.io/bbolt"
)

// DeliveryPrincipal is populated only by a verified, server-side delivery call.
// The host must still resolve its user ID against current endpoint permissions.
func DeliveryPrincipal(r *http.Request) (Principal, bool) {
	p, ok := r.Context().Value(deliverySystemPrincipal{}).(Principal)
	return p, ok
}
func (s *Service) deliveryInternalRequest(r *http.Request, actor int, endpoints ...int) *http.Request {
	principal := Principal{UserID: actor}
	endpoint := 0
	if len(endpoints) > 0 {
		endpoint = endpoints[0]
	}
	if s.opts.InspectAccess != nil {
		resolved, allowed, err := s.opts.InspectAccess(actor, endpoint)
		if err == nil && allowed {
			principal = resolved
		} else {
			principal = Principal{}
		}
	}
	return r.Clone(context.WithValue(r.Context(), deliverySystemPrincipal{}, principal))
}

func (s *Service) deliveryRoutes(router *mux.Router, add func(string, string, func(http.ResponseWriter, *http.Request) error, bool)) {
	add("GET", "/delivery/catalog", s.deliveryCatalog, false)
	add("GET", "/model-requests", s.deliveryRequests, false)
	add("POST", "/model-requests", s.deliveryRequests, false)
	add("GET", "/model-requests/{id}", s.deliveryRequest, false)
	add("POST", "/model-requests/{id}/messages", s.deliveryMessage, false)
	add("GET", "/admin/model-requests", s.deliveryRequests, true)
	add("POST", "/admin/model-requests/{id}/resolve", s.deliveryResolve, true)
	add("POST", "/admin/model-requests/{id}/retry", s.deliveryResolve, true)
	for _, kind := range []string{"recipes", "policies"} {
		handler := s.deliveryRecipes
		if kind == "policies" {
			handler = s.deliveryPolicies
		}
		add("GET", "/admin/delivery/"+kind, handler, true)
		add("PUT", "/admin/delivery/"+kind+"/{id}", handler, true)
	}
	add("GET", "/admin/delivery/settings", s.deliverySettings, true)
	for _, kind := range []string{"jobs", "artifacts"} {
		k := kind
		for _, prefix := range []string{"/delivery/", "/admin/delivery/"} {
			admin := strings.HasPrefix(prefix, "/admin")
			add("GET", prefix+k, func(w http.ResponseWriter, r *http.Request) error { return s.deliveryRead(w, r, k, false) }, admin)
			add("GET", prefix+k+"/{id}", func(w http.ResponseWriter, r *http.Request) error { return s.deliveryRead(w, r, k, true) }, admin)
		}
	}
	add("GET", "/delivery/artifacts/{id}/export", s.deliveryDownload, false)
	add("GET", "/delivery/artifacts/{id}/export-manifest", s.deliveryExport, false)
	add("POST", "/deployment-plans", s.deliveryPlanCreate, false)
	add("POST", "/deployment-plans/{id}/confirm", s.deliveryPlanConfirm, false)
	add("GET", "/rollouts", s.deliveryRollouts, false)
	add("GET", "/rollouts/{id}", s.deliveryRollouts, false)
	internal := func(path string, fn func(http.ResponseWriter, *http.Request) error) {
		router.Handle("/api/edgestack/internal/delivery/"+path, route(fn)).Methods("POST")
	}
	internal("jobs/claim", s.deliveryClaim)
	internal("jobs/{id}/heartbeat", s.deliveryHeartbeat)
	internal("jobs/{id}/result", s.deliveryResult)
	internal("jobs/{id}/validation/start", s.deliveryValidationStart)
	internal("jobs/{id}/validation/status", s.deliveryValidationStatus)
	internal("jobs/{id}/validation/complete", s.deliveryValidationComplete)
}
func (s *Service) deliverySettings(w http.ResponseWriter, r *http.Request) error {
	if err := s.admin(r); err != nil {
		return err
	}
	file := s.opts.DeliveryWorkerTokenFile
	if file == "" {
		file = os.Getenv("EDGESTACK_DELIVERY_WORKER_TOKEN_FILE")
	}
	info, err := os.Lstat(file)
	configured := err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0077 == 0
	respond(w, 200, map[string]any{"enabled": s.deliveryEnabled(), "workerTokenConfigured": configured, "newTaskProviderConfigured": s.opts.DeliveryProvisioner != nil, "healthObserverConfigured": s.opts.DeliveryObserveHealth != nil, "heartbeatSeconds": 30, "claimSeconds": 120})
	return nil
}
func (s *Service) deliveryRead(w http.ResponseWriter, r *http.Request, kind string, single bool) error {
	principal, err := s.principal(r)
	if err != nil {
		return err
	}
	var result = []any{}
	err = s.db.View(func(tx *bolt.Tx) error {
		jobs, e := list[deliveryJobRecord](tx, "delivery-jobs")
		if e != nil {
			return e
		}
		for _, job := range jobs {
			request, e := read[ModelRequest](tx, "delivery-requests", job.RequestID)
			ownerAccess := e == nil && s.deliveryRequestAccess(tx, r, request) == nil
			if kind == "jobs" {
				if !ownerAccess {
					continue
				}
				if !single || job.ID == mux.Vars(r)["id"] {
					result = append(result, job.DeliveryJob)
				}
			} else if job.ArtifactID != "" {
				artifact, e := read[DeliveryArtifact](tx, "delivery-artifacts", job.ArtifactID)
				if e == nil && ((ownerAccess && (principal.Admin || s.deliveryArtifactPolicyAccess(tx, r, job))) || s.deliveryArtifactReadable(tx, r, artifact)) && (!single || artifact.ID == mux.Vars(r)["id"]) {
					result = append(result, artifact)
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if single {
		if len(result) == 0 {
			return fail(404, "not_found")
		}
		respond(w, 200, result[0])
	} else {
		respond(w, 200, map[string]any{"items": result})
	}
	return nil
}
func (s *Service) deliveryExport(w http.ResponseWriter, r *http.Request) error {
	recorder := &deliveryResponse{header: http.Header{}}
	if err := s.deliveryRead(recorder, r, "artifacts", true); err != nil {
		return err
	}
	var artifact DeliveryArtifact
	if err := json.Unmarshal(recorder.Bytes(), &artifact); err != nil {
		return err
	}
	respond(w, 200, map[string]any{"artifactId": artifact.ID, "digest": artifact.Digest, "image": artifact.Image, "offlineBundle": artifact.OfflineBundle, "files": artifact.Files, "reportDigest": artifact.ReportDigest})
	return nil
}

type deliveryResponse struct {
	bytes.Buffer
	header http.Header
	status int
}

func (w *deliveryResponse) Header() http.Header    { return w.header }
func (w *deliveryResponse) WriteHeader(status int) { w.status = status }
func (s *Service) deliveryPreview(r *http.Request, task Task, model ModelPackage, validation bool) (Preview, error) {
	body, _ := json.Marshal(map[string]any{"packageId": model.ID, "slot": model.Slot, "revision": task.Revision})
	request := mux.SetURLVars(r.Clone(r.Context()), map[string]string{"id": task.ID})
	request.Body = http.NoBody
	request.Body = ioReadCloser(body)
	if validation {
		request = validationRequest(request, validationIntent{model.ID, task.ID}, map[string]any{"packageId": model.ID, "slot": model.Slot, "revision": task.Revision})
	}
	response := &deliveryResponse{header: http.Header{}}
	err := s.preview(response, request)
	var preview Preview
	if err == nil {
		err = json.Unmarshal(response.Bytes(), &preview)
	}
	return preview, err
}

type deliveryReadCloser struct{ *bytes.Reader }

func (deliveryReadCloser) Close() error           { return nil }
func ioReadCloser(data []byte) deliveryReadCloser { return deliveryReadCloser{bytes.NewReader(data)} }
func (s *Service) deliveryChange(r *http.Request, taskID, previewID string, validation bool) (Operation, error) {
	request := mux.SetURLVars(r.Clone(r.Context()), map[string]string{"id": taskID})
	body := map[string]any{"previewId": previewID, "confirm": true}
	raw, _ := json.Marshal(body)
	request.Body = ioReadCloser(raw)
	if validation {
		var saved previewRecord
		err := s.db.View(func(tx *bolt.Tx) error {
			var e error
			saved, e = read[previewRecord](tx, "previews", previewID)
			return e
		})
		if err != nil {
			return Operation{}, err
		}
		request = validationRequest(request, validationIntent{saved.Preview.PackageID, taskID}, body)
	}
	response := &deliveryResponse{header: http.Header{}}
	err := s.changeModel(response, request)
	var op Operation
	if err == nil {
		err = json.Unmarshal(response.Bytes(), &op)
	}
	return op, err
}
