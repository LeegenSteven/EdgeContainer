package edgestackbusiness

import (
	"context"
	"strings"
	"testing"

	"github.com/portainer/portainer/api/edgestack"
)

func TestModelValidationChecksActualImageAndApprovedParameters(t *testing.T) {
	image := "example.invalid/voice@sha256:" + strings.Repeat("a", 64)
	for _, tc := range []struct {
		name, actualImage, actualWindow string
		allow                           bool
	}{
		{"exact-package", image, "4", true},
		{"same-weights-old-image", "example.invalid/voice@sha256:" + strings.Repeat("b", 64), "4", false},
		{"same-image-old-parameters", image, "2", false},
		{"unconfirmed-image-id-only", "sha256:" + strings.Repeat("a", 64), "4", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, client, task := mountFixture()
			task.ModelEnvKeys = []string{"ASR_WINDOW"}
			client.actual.Config.Image = tc.actualImage
			client.actual.Config.Env = []string{"ASR_WINDOW=" + tc.actualWindow, "PRIVATE_ENV=never-return"}
			model := edgestack.ModelPackage{Image: image, Environment: map[string]string{"ASR_WINDOW": "4"}}
			err := modelRuntimeVerifier(provider)(context.Background(), task, model)
			if (err == nil) != tc.allow {
				t.Fatalf("verification=%v allow=%v", err, tc.allow)
			}
			snapshot := provider.snapshot(context.Background(), task)
			if _, exists := snapshot.modelEnvironment["PRIVATE_ENV"]; exists {
				t.Fatal("private environment entered cached projection")
			}
		})
	}
}

func TestMissingRuntimeProviderCannotValidateModelImage(t *testing.T) {
	if modelRuntimeVerifier(nil)(context.Background(), edgestack.Task{}, edgestack.ModelPackage{}) == nil {
		t.Fatal("missing inspect provider allowed model validation")
	}
}
