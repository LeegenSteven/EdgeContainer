package edgestackbusiness

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/api/dataservices"
	dockerclient "github.com/portainer/portainer/api/docker/client"
	"github.com/portainer/portainer/api/edgestack"
	"github.com/portainer/portainer/api/http/offlinegate"
	"github.com/portainer/portainer/api/http/security"
	edgestackservice "github.com/portainer/portainer/api/internal/edge/edgestacks"
)

// New keeps business authorization additive to the existing Portainer session
// and endpoint/group authorization. No existing administration route changes.
func New(ctx context.Context, bouncer security.BouncerService, store dataservices.DataStore, files portainer.FileService, stacks *edgestackservice.Service, factory *dockerclient.ClientFactory, gates ...*offlinegate.OfflineGate) (*edgestack.Service, error) {
	dispatcher := &Dispatcher{store: store, files: files}
	bootstrapDisabled, err := edgestack.StartupBootstrapDisabled(filepath.Join(files.GetDatastorePath(), "edgestack"))
	if err != nil {
		return nil, errors.New("unable to check business recovery state")
	}
	validateACL := func(acl edgestack.ACL) error {
		for _, id := range acl.UserIDs {
			if id <= 0 {
				return errors.New("invalid business user")
			}
			if _, err := store.User().Read(portainer.UserID(id)); err != nil {
				return errors.New("business user does not exist")
			}
		}
		for _, id := range acl.TeamIDs {
			if id <= 0 {
				return errors.New("invalid business team")
			}
			if _, err := store.Team().Read(portainer.TeamID(id)); err != nil {
				return errors.New("business team does not exist")
			}
		}
		return nil
	}
	var lease func() (func(), error)
	if len(gates) > 0 && gates[0] != nil {
		lease = func() (func(), error) { return gates[0].ReadLease(time.Minute) }
	}
	var voiceTestHTML []byte
	mounts := newMountProvider(store, factory)
	if path := os.Getenv("EDGESTACK_VOICE_TEST_FILE"); path != "" {
		var err error
		voiceTestHTML, err = os.ReadFile(path)
		if err != nil {
			return nil, errors.New("unable to read voice test page")
		}
	}
	inspectAccess := func(userID, endpointID int) (edgestack.Principal, bool, error) {
		user, err := store.User().Read(portainer.UserID(userID))
		if err != nil {
			return edgestack.Principal{}, false, err
		}
		memberships, err := store.TeamMembership().TeamMembershipsByUserID(user.ID)
		if err != nil {
			return edgestack.Principal{}, false, err
		}
		p := edgestack.Principal{UserID: userID, Admin: user.Role == portainer.AdministratorRole}
		for _, member := range memberships {
			p.TeamIDs = append(p.TeamIDs, int(member.TeamID))
		}
		endpoint, err := store.Endpoint().Endpoint(portainer.EndpointID(endpointID))
		if err != nil {
			return p, false, err
		}
		group, err := store.EndpointGroup().Read(endpoint.GroupID)
		if err != nil {
			return p, false, err
		}
		return p, p.Admin || security.AuthorizedEndpointAccess(endpoint, group, user.ID, memberships), nil
	}
	provisioner, err := newDeliveryProvisioner(store, files, stacks, os.Getenv("EDGESTACK_DELIVERY_TEMPLATES_FILE"))
	if err != nil {
		return nil, err
	}
	service, err := edgestack.Open(filepath.Join(files.GetDatastorePath(), "edgestack", "business.db"), edgestack.Options{
		ValidateACL: validateACL, BackgroundWriteLease: lease,
		InspectAccess: inspectAccess,
		Authenticated: bouncer.AuthenticatedAccess, AdminAccess: bouncer.AdminAccess,
		Authenticate: func(r *http.Request) (edgestack.Principal, error) {
			// Re-run the original authenticator when delayed results are
			// returned, so expired/revoked cookies and API keys fail closed.
			var token *portainer.TokenData
			probe := &sessionProbe{header: make(http.Header)}
			bouncer.AuthenticatedAccess(http.HandlerFunc(func(_ http.ResponseWriter, fresh *http.Request) { token, _ = security.RetrieveTokenData(fresh) })).ServeHTTP(probe, r)
			if token == nil {
				return edgestack.Principal{}, errors.New("session no longer valid")
			}
			// Read the identity and memberships again when returning a delayed
			// summary, so a revocation during the provider call takes effect.
			user, err := store.User().Read(token.ID)
			if err != nil {
				return edgestack.Principal{}, err
			}
			if user.Role != token.Role {
				return edgestack.Principal{}, errors.New("session role changed")
			}
			memberships, err := store.TeamMembership().TeamMembershipsByUserID(user.ID)
			if err != nil {
				return edgestack.Principal{}, err
			}
			p := edgestack.Principal{UserID: int(user.ID), Admin: user.Role == portainer.AdministratorRole}
			for _, m := range memberships {
				p.TeamIDs = append(p.TeamIDs, int(m.TeamID))
			}
			return p, nil
		},
		EndpointAccess: func(r *http.Request, id int) bool {
			if actor, internal := edgestack.DeliveryPrincipal(r); internal {
				_, allowed, err := inspectAccess(actor.UserID, id)
				return err == nil && allowed
			}
			endpoint, err := store.Endpoint().Endpoint(portainer.EndpointID(id))
			return err == nil && bouncer.AuthorizedEndpointOperation(r, endpoint) == nil
		},
		Dispatcher: dispatcher, ValidateTask: dispatcher.ValidateTask, Provider: edgestack.NewDeepSeekProviderFromEnv(),
		VoiceTestHTML:       voiceTestHTML,
		DeliveryProvisioner: provisioner, DeliveryObserveHealth: deliveryHealthObserver(mounts),
		MountProvider: mounts, VerifyModelRuntime: modelRuntimeVerifier(mounts),
		DeviceStatus: func(_ *http.Request, id int) (bool, time.Time) {
			endpoint, err := store.Endpoint().Endpoint(portainer.EndpointID(id))
			if err != nil || endpoint.LastCheckInDate == 0 {
				return false, time.Time{}
			}
			last := time.Unix(endpoint.LastCheckInDate, 0).UTC()
			return time.Since(last) < 2*time.Minute, last
		},
	})
	if err != nil {
		return nil, errors.New("unable to open EdgeStack business database")
	}
	// Restoring must never replay operator deployment configuration. Older
	// archives without a business registry require explicit administrator import.
	if path := os.Getenv("EDGESTACK_BOOTSTRAP_FILE"); path != "" && !bootstrapDisabled {
		if err := bootstrap(ctx, service, store, files, stacks, path); err != nil {
			service.Close()
			return nil, err
		}
	}
	if err := service.ApplyRestorePolicy(func(id int) bool { _, err := store.Endpoint().Endpoint(portainer.EndpointID(id)); return err == nil }, func(acl edgestack.ACL) bool { return validateACL(acl) == nil }); err != nil {
		service.Close()
		return nil, errors.New("unable to finalize business database restoration")
	}
	if err := service.Prune(); err != nil {
		service.Close()
		return nil, errors.New("unable to apply business retention policy")
	}
	execution, err := newDeliveryExecutionAuthority(service, store)
	if err != nil {
		service.Close()
		return nil, err
	}
	execution.mounts, _ = mounts.(*mountProvider)
	execution.adminAccess = bouncer.AdminAccess
	service.SetDeliveryExecutionAuthority(execution)
	dispatcher.execution = execution
	dispatcher.requireExecution = os.Getenv("EDGESTACK_DELIVERY_ENABLED") == "true"
	if p, ok := provisioner.(*deliveryProvisioner); ok {
		p.PrepareExecution = execution.Prepare
		p.CheckExecution = execution.Preview
		p.mounts, _ = mounts.(*mountProvider)
	}
	go execution.Run(ctx)
	go service.RunRetention(ctx)
	go service.RunDelivery(ctx)
	return service, nil
}

type sessionProbe struct{ header http.Header }

func (p *sessionProbe) Header() http.Header          { return p.header }
func (*sessionProbe) Write(data []byte) (int, error) { return len(data), nil }
func (*sessionProbe) WriteHeader(int)                {}
