package edgestackbusiness

import (
	"github.com/docker/docker/api/types/container"
	"github.com/portainer/portainer/api/edgestack"
	"go.yaml.in/yaml/v3"
	"strings"
	"testing"
)

func TestDeliveryObserverSecretIsScopedDeterministicAndKeepsApplicationData(t *testing.T) {
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
	secret := mappingValue(mappingValue(root.Content[0], "secrets"), "edgestack-model-observer")
	if mappingValue(secret, "content").Value != spec.ObserverToken {
		t.Fatal("missing scoped secret")
	}
	for _, key := range []string{"EDGESTACK_MODEL_OBSERVER_TASK_ID", "EDGESTACK_MODEL_OBSERVER_DEVICE_ID", "EDGESTACK_MODEL_OBSERVER_SLOT"} {
		if mappingValue(env, key) == nil {
			t.Fatalf("missing binding %s", key)
		}
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
