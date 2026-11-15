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
	"github.com/portainer/portainer/api/http/security"
	edgestackservice "github.com/portainer/portainer/api/internal/edge/edgestacks"
)

// New keeps business authorization additive to the existing Portainer session
// and endpoint/group authorization. No existing administration route changes.
func New(ctx context.Context, bouncer security.BouncerService, store dataservices.DataStore, files portainer.FileService, stacks *edgestackservice.Service, factory *dockerclient.ClientFactory) (*edgestack.Service, error) {
	dispatcher := &Dispatcher{store: store, files: files}
	var voiceTestHTML []byte
	if path := os.Getenv("EDGESTACK_VOICE_TEST_FILE"); path != "" {
		var err error
		voiceTestHTML, err = os.ReadFile(path)
		if err != nil {
			return nil, errors.New("unable to read voice test page")
		}
	}
	service, err := edgestack.Open(filepath.Join(files.GetDatastorePath(), "edgestack", "business.db"), edgestack.Options{
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
			endpoint, err := store.Endpoint().Endpoint(portainer.EndpointID(id))
			return err == nil && bouncer.AuthorizedEndpointOperation(r, endpoint) == nil
		},
		Dispatcher: dispatcher, ValidateTask: dispatcher.ValidateTask, Provider: edgestack.NewDeepSeekProviderFromEnv(),
		VoiceTestHTML: voiceTestHTML,
		MountProvider: newMountProvider(store, factory),
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
	if path := os.Getenv("EDGESTACK_BOOTSTRAP_FILE"); path != "" {
		if err := bootstrap(ctx, service, store, files, stacks, path); err != nil {
			service.Close()
			return nil, err
		}
	}
	go service.RunRetention(ctx)
	return service, nil
}

type sessionProbe struct{ header http.Header }

func (p *sessionProbe) Header() http.Header          { return p.header }
func (*sessionProbe) Write(data []byte) (int, error) { return len(data), nil }
func (*sessionProbe) WriteHeader(int)                {}
