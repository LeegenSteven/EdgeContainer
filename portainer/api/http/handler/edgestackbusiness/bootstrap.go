package edgestackbusiness

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/api/dataservices"
	"github.com/portainer/portainer/api/edgestack"
	"github.com/portainer/portainer/api/filesystem"
	"github.com/portainer/portainer/api/internal/edge"
	edgestackservice "github.com/portainer/portainer/api/internal/edge/edgestacks"
	"github.com/portainer/portainer/api/stacks/stackutils"
)

type bootstrapDeployment struct {
	Name          string `json:"name"`
	EndpointID    int    `json:"endpointId"`
	EdgeGroupName string `json:"edgeGroupName"`
	Compose       string `json:"compose"`
}
type operatorConfig struct {
	edgestack.BootstrapConfig
	Deployments []bootstrapDeployment `json:"deployments"`
}

func bootstrap(ctx context.Context, s *edgestack.Service, store dataservices.DataStore, files portainer.FileService, stacks *edgestackservice.Service, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return errors.New("unable to open EdgeStack operator configuration")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return errors.New("EdgeStack operator configuration must be a private regular file (0600)")
	}
	var config operatorConfig
	decoder := json.NewDecoder(io.LimitReader(f, 4<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return errors.New("invalid EdgeStack operator configuration")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("invalid trailing operator configuration")
	}
	registered := map[string]bool{}
	registeredDeployments := map[string]bool{}
	for i := range config.Tasks {
		target, exists, e := s.BootstrapTaskTarget(config.Tasks[i].ID)
		if e != nil {
			return e
		}
		if exists {
			registered[config.Tasks[i].ID] = true
			registeredDeployments[config.Tasks[i].Target.StackName] = true
			config.Tasks[i].Target = target
		}
	}
	for _, deployment := range config.Deployments {
		if registeredDeployments[deployment.Name] {
			continue
		}
		if !strings.HasPrefix(deployment.Name, "edgestack-") || deployment.EndpointID <= 0 || deployment.EdgeGroupName == "" || deployment.Compose == "" {
			return errors.New("invalid isolated bootstrap deployment")
		}
		if err := stackutils.ValidateEdgeStackComposeContent(ctx, portainer.EdgeStackDeploymentCompose, []byte(deployment.Compose)); err != nil {
			return err
		}
		if err := store.UpdateTx(func(tx dataservices.DataStoreTx) error {
			relations, err := edge.FetchEndpointRelationsConfig(tx)
			if err != nil {
				return err
			}
			var groupID portainer.EdgeGroupID
			for _, g := range relations.EdgeGroups {
				if g.Name == deployment.EdgeGroupName {
					groupID = g.ID
				}
			}
			if groupID == 0 {
				return errors.New("bootstrap Edge group not found")
			}
			endpoints, err := edge.EdgeStackRelatedEndpoints([]portainer.EdgeGroupID{groupID}, relations.Endpoints, relations.EndpointGroups, relations.EdgeGroups)
			if err != nil || len(endpoints) != 1 || int(endpoints[0]) != deployment.EndpointID {
				return errors.New("bootstrap Edge group must target exactly the approved endpoint")
			}
			existing, err := tx.EdgeStack().EdgeStacks()
			if err != nil {
				return err
			}
			for _, stack := range existing {
				if strings.EqualFold(stack.Name, deployment.Name) {
					return nil
				}
			}
			root, err := document([]byte(deployment.Compose))
			if err != nil {
				return err
			}
			services := mappingValue(root.Content[0], "services")
			if services == nil || len(services.Content) != 2 {
				return errors.New("bootstrap deployment must contain one service")
			}
			if _, err := serviceNode([]byte(deployment.Compose), services.Content[0].Value); err != nil {
				return err
			}
			endpoint, err := tx.Endpoint().Endpoint(portainer.EndpointID(deployment.EndpointID))
			if err != nil || endpoint.Type != portainer.EdgeAgentOnDockerEnvironment {
				return errors.New("bootstrap endpoint must be Docker Edge")
			}
			stack, err := stacks.BuildEdgeStack(tx, deployment.Name, portainer.EdgeStackDeploymentCompose, []portainer.EdgeGroupID{groupID}, nil, false)
			if err != nil {
				return err
			}
			_, err = stacks.PersistEdgeStack(tx, stack, func(folder string, _ []portainer.EndpointID) (string, string, string, error) {
				project, err := files.StoreEdgeStackFileFromBytes(folder, filesystem.ComposeFileDefaultName, []byte(deployment.Compose))
				return filesystem.ComposeFileDefaultName, "", project, err
			})
			return err
		}); err != nil {
			return err
		}
	}
	if err := store.ViewTx(func(tx dataservices.DataStoreTx) error {
		stacks, err := tx.EdgeStack().EdgeStacks()
		if err != nil {
			return err
		}
		for i := range config.Tasks {
			task := &config.Tasks[i]
			if registered[task.ID] {
				continue
			}
			if task.Target.StackName != "" {
				found := false
				for _, stack := range stacks {
					if stack.Name == task.Target.StackName {
						if task.Target.StackID != 0 && task.Target.StackID != int(stack.ID) {
							return errors.New("bootstrap target ID/name mismatch")
						}
						task.Target.StackID = int(stack.ID)
						found = true
						break
					}
				}
				if !found {
					return errors.New("bootstrap target stack name not found")
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return s.Bootstrap(ctx, config.BootstrapConfig)
}
