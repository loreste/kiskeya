//go:build pbxtest

// Integration test that registers the real SIPEngine against a live PBX.
// Excluded from normal builds/tests. Credentials come from the environment so no
// secrets live in source. Run explicitly, e.g.:
//
//	KISKEYA_TEST_USER=1001 KISKEYA_TEST_PASS=secret KISKEYA_TEST_DOMAIN=pbx.example.com \
//	  go test -tags pbxtest -run TestPBXRegister -v
package main

import (
	"os"
	"testing"
)

func TestPBXRegister(t *testing.T) {
	user := os.Getenv("KISKEYA_TEST_USER")
	pass := os.Getenv("KISKEYA_TEST_PASS")
	domain := os.Getenv("KISKEYA_TEST_DOMAIN")
	if user == "" || pass == "" || domain == "" {
		t.Skip("set KISKEYA_TEST_USER/PASS/DOMAIN to run the live PBX registration test")
	}
	proto := os.Getenv("KISKEYA_TEST_PROTO")
	if proto == "" {
		proto = "udp"
	}

	se := &SIPEngine{
		app:       &App{}, // nil ctx => EmitEvent/LogSIP are no-ops
		regState:  StateIdle,
		callState: StateIdle,
		localIP:   getLocalIP(),
	}
	se.account = &SIPAccount{Username: user, Password: pass, Domain: domain, Protocol: proto}

	if err := se.buildTransportUnlocked(0); err != nil {
		t.Fatalf("buildTransportUnlocked: %v", err)
	}
	defer se.StopServer()

	next, permanent := se.doRegister(120)
	t.Logf("doRegister -> nextRefresh=%v permanent=%v regState=%s regError=%q", next, permanent, se.regState, se.regError)

	if se.regState != StateRegistered {
		t.Fatalf("expected StateRegistered, got %s (%s)", se.regState, se.regError)
	}
	if next <= 0 {
		t.Errorf("expected a positive refresh interval on success, got %v", next)
	}
}
