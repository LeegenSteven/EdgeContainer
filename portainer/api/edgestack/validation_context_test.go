package edgestack

import (
	"context"
	"net/http/httptest"
	"testing"
)

func TestModelValidationContextIsPrivateAndRequiresBoundIntent(t *testing.T) {
	for _, ctx := range []context.Context{context.Background(), context.WithValue(context.Background(), "model-validation", true), context.WithValue(context.Background(), validationKey{}, validationIntent{})} {
		if IsModelValidation(ctx, "test-task", "candidate") {
			t.Fatal("unbound or external context was accepted as model validation")
		}
	}
	r := validationRequest(httptest.NewRequest("POST", "/", nil), validationIntent{"candidate", "test-task"}, map[string]any{})
	if !IsModelValidation(r.Context(), "test-task", "candidate") {
		t.Fatal("internal validation request lost its bound private intent")
	}
	for _, ids := range [][2]string{{"other-task", "candidate"}, {"test-task", "other-package"}, {"", "candidate"}, {"test-task", ""}} {
		if IsModelValidation(r.Context(), ids[0], ids[1]) {
			t.Fatal("validation intent was accepted for another task or package")
		}
	}
}
