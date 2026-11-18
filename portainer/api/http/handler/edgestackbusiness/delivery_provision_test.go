package edgestackbusiness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/schema"
	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/docker/api/types/container"
	"github.com/portainer/portainer/api/edgestack"
	"github.com/portainer/portainer/pkg/libstack"
	composeplugin "github.com/portainer/portainer/pkg/libstack/compose"
	"go.yaml.in/yaml/v3"
)

func TestDeliveryObserverConfigIsScopedDeterministicAndKeepsApplicationData(t *testing.T) {
	spec := edgestack.DeliveryProvisionSpec{TaskID: "task-1", Device: edgestack.Device{ID: "device-1"}, ObserverSourceID: "observer-1", ObserverToken: "test-scoped-secret", Package: edgestack.ModelPackage{Slot: "vision"}}
	compose := "services:\n  model:\n    image: example.invalid/model:latest\n    environment:\n      BUSINESS_SETTING: keep\n    volumes:\n      - business-data:/data\n    healthcheck:\n      test: [CMD, /opt/model-health]\nvolumes:\n  business-data: {}\n"
	first, err := deliveryObserverCompose(compose, "model", spec, "https://server.invalid")
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		again, err := deliveryObserverCompose(compose, "model", spec, "https://server.invalid")
		if err != nil || again != first {
			t.Fatal("reconciliation changed compose")
		}
	}
	root, err := serviceNode([]byte(first), "model")
	if err != nil {
		t.Fatal(err)
	}
	service := mappingValue(mappingValue(root.Content[0], "services"), "model")
	env := mappingValue(service, "environment")
	encoded, _ := yaml.Marshal(env)
	if strings.Contains(string(encoded), spec.ObserverToken) {
		t.Fatal("bearer token entered environment")
	}
	if mappingValue(env, "BUSINESS_SETTING").Value != "keep" || mappingValue(service, "volumes").Content[0].Value != "business-data:/data" {
		t.Fatal("application data mutated")
	}
	config := mappingValue(mappingValue(root.Content[0], "configs"), "edgestack-model-observer")
	if mappingValue(config, "content").Value != spec.ObserverToken {
		t.Fatal("missing scoped config file")
	}
	if mappingValue(root.Content[0], "secrets") != nil || mappingValue(service, "secrets") != nil {
		t.Fatal("unsupported secrets.content injection returned")
	}
	serviceEncoded, _ := yaml.Marshal(service)
	if strings.Contains(string(serviceEncoded), spec.ObserverToken) {
		t.Fatal("bearer token entered a service environment, image or build argument")
	}
	for _, key := range []string{"EDGESTACK_MODEL_OBSERVER_TASK_ID", "EDGESTACK_MODEL_OBSERVER_DEVICE_ID", "EDGESTACK_MODEL_OBSERVER_SLOT"} {
		if mappingValue(env, key) == nil {
			t.Fatalf("missing binding %s", key)
		}
	}
}

func TestDeliveryObserverConfigPassesPinnedSchemaLoaderAndActualComposeValidate(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	t.Setenv("NOT_AN_OBSERVER_CREDENTIAL", "must-not-be-interpolated")
	spec := edgestack.DeliveryProvisionSpec{TaskID: "task-1", Device: edgestack.Device{ID: "device-1"}, ObserverSourceID: "observer-1", ObserverToken: "literal-$NOT_AN_OBSERVER_CREDENTIAL-$$", Package: edgestack.ModelPackage{Slot: "vision"}}
	input := "services:\n  model:\n    image: example.invalid/model:latest\n    user: '1000:1000'\n    read_only: false\n    healthcheck:\n      test: [CMD, /opt/model-health]\n"
	out, err := deliveryObserverCompose(input, "model", spec, "https://server.invalid")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal([]byte(out), &document); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(document); err != nil {
		t.Fatalf("pinned Compose schema rejected observer config: %v", err)
	}
	dir := t.TempDir()
	project, err := loader.LoadWithContext(t.Context(), types.ConfigDetails{WorkingDir: dir, ConfigFiles: []types.ConfigFile{{Filename: "compose.yaml", Content: []byte(out)}}}, func(opts *loader.Options) { opts.SetProjectName("observer-config-test", true) })
	if err != nil {
		t.Fatal(err)
	}
	if project.Configs["edgestack-model-observer"].Content != spec.ObserverToken {
		t.Fatal("Compose interpolation changed the credential")
	}
	service := project.Services["model"]
	if service.User != "1000:1000" || len(service.Configs) != 1 {
		t.Fatal("runtime user or config reference changed")
	}
	reference := service.Configs[0]
	if reference.Source != "edgestack-model-observer" || reference.Target != "/run/secrets/edgestack-model-observer" || reference.UID != "0" || reference.GID != "0" || reference.Mode == nil || *reference.Mode != 0o444 {
		t.Fatalf("wrong runtime file permissions or path: %+v", reference)
	}
	for _, value := range service.Environment {
		if value != nil && strings.Contains(*value, spec.ObserverToken) {
			t.Fatal("credential entered environment")
		}
	}
	file := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(file, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	deployer := composeplugin.NewComposeDeployer()
	// macOS testing directories exceed the Unix socket length limit. This
	// deliberately absent short socket proves Validate needs no Docker engine.
	const missingSocket = "/tmp/edgestack-observer-no-engine.sock"
	if _, err := os.Stat(missingSocket); !os.IsNotExist(err) {
		t.Fatal("test Docker socket must be absent")
	}
	options := libstack.Options{ProjectName: "observer-config-test", Host: "unix://" + missingSocket}
	if err := deployer.Validate(t.Context(), []string{file}, options); err != nil {
		t.Fatalf("actual Compose Validate rejected config without any Docker engine: %v", err)
	}
	// This is the exact compatibility regression: YAML parsing accepts the old
	// secrets.content form, but the pinned Compose schema and Agent do not.
	old := "services:\n  model:\n    image: example.invalid/model:latest\n    secrets: [edgestack-model-observer]\nsecrets:\n  edgestack-model-observer:\n    content: credential\n"
	document = nil
	if err := yaml.Unmarshal([]byte(old), &document); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(document); err == nil {
		t.Fatal("pinned schema unexpectedly accepted secrets.content")
	}
	if err := os.WriteFile(file, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := deployer.Validate(t.Context(), []string{file}, options); err == nil || !strings.Contains(err.Error(), "content") {
		t.Fatalf("actual Compose Validate did not reject secrets.content: %v", err)
	}
}

func TestDeliveryObserverRefusesReadonlyRootAndForeignConfigConflicts(t *testing.T) {
	spec := edgestack.DeliveryProvisionSpec{ObserverSourceID: "source", ObserverToken: "token"}
	service := "services:\n  model:\n    image: example.invalid/model\n    healthcheck: {test: [CMD, /opt/model-health]}\n"
	for _, tc := range []struct{ name, input, reason string }{
		{"readonly", service + "    read_only: true\n", "writable_root"},
		{"unresolved-readonly", service + "    read_only: ${READ_ONLY}\n", "writable_root"},
		{"readonly-alias", "x-readonly: &flag true\n" + service + "    read_only: *flag\n", "writable_root"},
		{"inherited-readonly", "x-base: &base\n  read_only: true\n" + service + "    <<: *base\n", "inheritance"},
		{"foreign-definition", service + "configs:\n  edgestack-model-observer: {content: foreign}\n", "config_conflict"},
		{"foreign-target", service + "    configs:\n      - {source: business, target: /run/secrets/edgestack-model-observer}\nconfigs:\n  business: {content: keep}\n", "config_conflict"},
		{"foreign-parent-target", service + "    configs:\n      - {source: business, target: /run/secrets}\nconfigs:\n  business: {content: keep}\n", "config_conflict"},
		{"legacy-secret-target", service + "    secrets:\n      - {source: business, target: edgestack-model-observer}\nsecrets:\n  business: {environment: BUSINESS_SECRET}\n", "config_conflict"},
		{"legacy-short-secret", service + "    secrets: [edgestack-model-observer]\nsecrets:\n  edgestack-model-observer: {environment: BUSINESS_SECRET}\n", "config_conflict"},
		{"invalid-config-map", service + "configs: []\n", "config_invalid"},
		{"invalid-service-config", service + "    configs: {}\n", "config_mount_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := deliveryObserverCompose(tc.input, "model", spec, "https://server.invalid"); err == nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("unsafe template accepted or wrong rejection: %v", err)
			}
		})
	}
	input := service + "    configs:\n      - {source: business, target: /etc/business}\nconfigs:\n  business: {content: keep}\n"
	out, err := deliveryObserverCompose(input, "model", spec, "https://server.invalid")
	if err != nil {
		t.Fatal(err)
	}
	project, err := loader.LoadWithContext(t.Context(), types.ConfigDetails{WorkingDir: t.TempDir(), ConfigFiles: []types.ConfigFile{{Content: []byte(out)}}}, func(opts *loader.Options) { opts.SetProjectName("observer-preserve-test", true) })
	if err != nil || project.Configs["business"].Content != "keep" || len(project.Services["model"].Configs) != 2 || project.Services["model"].Configs[0].Target != "/etc/business" {
		t.Fatalf("unrelated business config changed: %v", err)
	}
}
func TestDeliveryObserverRefusesMissingTrustAndHealth(t *testing.T) {
	spec := edgestack.DeliveryProvisionSpec{ObserverSourceID: "source", ObserverToken: "token"}
	for _, tc := range []struct {
		compose, url string
		spec         edgestack.DeliveryProvisionSpec
	}{
		{"services:\n  model:\n    image: example.invalid/model\n", "https://server.invalid", spec},
		{"services:\n  model:\n    image: example.invalid/model\n    healthcheck: {test: [CMD, true]}\n", "https://secret@server.invalid", spec},
		{"services:\n  model:\n    image: example.invalid/model\n    healthcheck: {test: [CMD, true]}\n", "https://server.invalid", edgestack.DeliveryProvisionSpec{}},
	} {
		if _, err := deliveryObserverCompose(tc.compose, "model", tc.spec, tc.url); err == nil {
			t.Fatal("unsafe observer accepted")
		}
	}
}

func TestDeliveryRollbackDoesNotTreatPausedOrRestartingAsStopped(t *testing.T) {
	target := edgestack.Target{StackID: 17, ServiceID: "model"}
	for _, state := range []string{"running", "paused", "restarting", "created", "removing", "exited", "dead"} {
		rows := []container.Summary{{State: state, Labels: map[string]string{edgeStackLabel: "17", composeServiceLabel: "model"}}}
		if deliveryContainersStopped(rows, target) != (state == "exited" || state == "dead") {
			t.Fatalf("incorrect stop proof for %s", state)
		}
	}
	if deliveryContainersStopped([]container.Summary{{State: "exited", Labels: map[string]string{edgeStackLabel: "18", composeServiceLabel: "model"}}}, target) {
		t.Fatal("foreign container accepted as stop proof")
	}
}
