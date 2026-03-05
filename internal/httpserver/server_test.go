package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServer_HandleHealth(t *testing.T) {
	srv := New(nil, nil, nil, nil, nil)
	mux := http.NewServeMux()
	srv.Routes(mux)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("статус: got %d, want %d", rr.Code, http.StatusOK)
	}
	if body := rr.Body.String(); body != "ok" {
		t.Errorf("тело: got %q, want %q", body, "ok")
	}
}
