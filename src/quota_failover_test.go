package main

import (
	"errors"
	"net/http"
	"testing"
)

func TestNativeExactPinSelectionErrorAllowsCPAPreDispatchFailures(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		message string
	}{
		{"legacy no auth", http.StatusServiceUnavailable, "no auth available"},
		{"enriched auth unavailable", http.StatusServiceUnavailable, "auth_unavailable: no auth available (providers=codex, model=gpt-6-sol)"},
		{"auth unavailable with prior quota error", http.StatusServiceUnavailable, "auth_unavailable: no auth available (providers=codex, model=gpt-6-sol; last upstream error: quota exhausted)"},
		{"auth not found", http.StatusServiceUnavailable, "auth_not_found: no auth available (providers=codex, model=gpt-6-sol)"},
		{"no credentials", 0, "auth_not_found: no auth candidates"},
		{"no provider", 0, "provider_not_found: no provider supplied"},
		{"no provider 503", http.StatusServiceUnavailable, "provider_not_found: no provider supplied"},
		{"quota cooldown", http.StatusTooManyRequests, `{"error":{"code":"model_cooldown","message":"All credentials for model gpt-6-sol are cooling down","provider":"codex","reset_seconds":3600}}`},
		{"quota cooldown with previous error", http.StatusTooManyRequests, `{"error":{"code":"model_cooldown","message":"All credentials for model gpt-6-sol are cooling down (last error: quota exhausted)","last_upstream_error":"quota exhausted"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := &hostCallbackError{Code: "host_call_failed", HTTPStatus: tc.status, Message: tc.message}
			if !isNativeExactPinSelectionError(err) {
				t.Fatalf("CPA selection failure rejected: status=%d message=%q", tc.status, tc.message)
			}
			if got := failureActionV4(defaultFailover(), statusFromError(err), err); got != failNext {
				t.Fatalf("selection failure action=%q, want next", got)
			}
		})
	}
}

func TestNativeExactPinSelectionErrorRejectsUnclaimedDispatchAndUntrustedErrors(t *testing.T) {
	cases := []struct {
		name    string
		code    string
		status  int
		message string
	}{
		{"unclaimed upstream 429", "host_call_failed", http.StatusTooManyRequests, "upstream returned HTTP 429"},
		{"generic JSON 429", "host_call_failed", http.StatusTooManyRequests, `{"error":{"code":"rate_limit_exceeded","message":"quota exceeded"}}`},
		{"misleading cooldown text", "host_call_failed", http.StatusTooManyRequests, "model_cooldown: quota exceeded"},
		{"unclaimed upstream 503", "host_call_failed", http.StatusServiceUnavailable, "simulated upstream 503"},
		{"auth-looking upstream error", "host_call_failed", http.StatusServiceUnavailable, "upstream auth_unavailable: no auth available"},
		{"wrong status", "host_call_failed", http.StatusUnauthorized, "auth_unavailable: no auth available"},
		{"untrusted callback code", "some_other_error", http.StatusServiceUnavailable, "auth_unavailable: no auth available"},
		{"generic no auth without known status", "host_call_failed", 0, "no auth available"},
		{"unsupported selector code", "host_call_failed", http.StatusServiceUnavailable, "auth_error: no auth available"},
		{"invalid JSON cooldown", "host_call_failed", http.StatusTooManyRequests, `{"error":{"code":"model_cooldown"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := &hostCallbackError{Code: tc.code, HTTPStatus: tc.status, Message: tc.message}
			if isNativeExactPinSelectionError(err) {
				t.Fatalf("unclaimed host error incorrectly classified as safe selection failure: %+v", err)
			}
		})
	}
	if isNativeExactPinSelectionError(errors.New("auth_unavailable: no auth available")) {
		t.Fatal("plain unverified error was treated as a trusted host selection failure")
	}
}
