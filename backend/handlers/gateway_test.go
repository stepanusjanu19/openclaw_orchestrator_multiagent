package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"multiagent-platform/internal/gateway"
)

func TestVerifyMetaSignature(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	secret := "super-secret"
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if err := verifyMetaSignature(signature, secret, body); err != nil {
		t.Fatalf("expected valid signature, got %v", err)
	}
	if err := verifyMetaSignature(signature, "wrong", body); err == nil {
		t.Fatalf("expected invalid signature error")
	}
}

func TestHTTPGatewayAuthTokenIsEnforced(t *testing.T) {
	h := &GatewayHandler{}
	req := httptest.NewRequest(http.MethodPost, "/api/webhook/main/http", strings.NewReader(`{"message":"hi","sender":"user-1"}`))
	rec := httptest.NewRecorder()

	h.handleHTTPGenericWebhook(rec, req, "main", &gateway.Gateway{
		AgentID: "main",
		Config: map[string]string{
			"auth_token": "expected-token",
		},
	})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing bearer token, got %d", rec.Code)
	}
}
