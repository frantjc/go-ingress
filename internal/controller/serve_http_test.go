package controller_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/frantjc/go-ingress/internal/controller"
	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func FuzzIngressController_ServeHTTP_NotFound(f *testing.F) {
	f.Add(http.MethodGet, "/notfound")
	f.Add(http.MethodPost, "/alsonotfound")
	f.Add(http.MethodHead, "/isheadfound")
	f.Fuzz(func(t *testing.T, method, target string) {
		client := fake.NewFakeClient()
		ctrl := &controller.IngressController{Client: client}
		recorder := httptest.NewRecorder()
		ctrl.ServeHTTP(
			recorder,
			httptest.NewRequestWithContext(
				t.Context(),
				method,
				target,
				nil,
			),
		)
		result := recorder.Result()
		defer assert.NoError(t, result.Body.Close())
		assert.Equal(t, http.StatusNotFound, result.StatusCode)
		body, err := io.ReadAll(result.Body)
		assert.NoError(t, err)
		assert.Equal(t, "404 page not found\n", string(body))
	})
}
