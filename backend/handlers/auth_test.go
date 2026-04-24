package handlers

import (
	"net/http/httptest"
	"testing"
)

func TestRequiresCSRFFollowsMethodAndRoute(t *testing.T) {
	loginReq := httptest.NewRequest("POST", "/api/auth/login", nil)
	if requiresCSRF(loginReq) {
		t.Fatalf("public login route should not require CSRF")
	}

	mutatingReq := httptest.NewRequest("POST", "/api/agents/main/start?token=abc", nil)
	if !requiresCSRF(mutatingReq) {
		t.Fatalf("protected mutating route should require CSRF")
	}

	otherGetReq := httptest.NewRequest("GET", "/api/agents/main/status?token=abc", nil)
	if requiresCSRF(otherGetReq) {
		t.Fatalf("GET route should not require CSRF")
	}
}
