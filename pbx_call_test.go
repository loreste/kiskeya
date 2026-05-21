//go:build pbxtest

// Live outbound-call integration test against a real PBX, exercising the full
// DialogClient path: REGISTER, INVITE (+digest), provisional/ringing, 200 OK,
// SDP negotiation, ACK, established dialog, then BYE.
//
//	KISKEYA_TEST_USER=1001 KISKEYA_TEST_PASS=… KISKEYA_TEST_DOMAIN=pbx.example.com \
//	  KISKEYA_TEST_TARGET=1002 go test -tags pbxtest -run TestPBXCall -v -timeout 120s
package main

import (
	"fmt"
	"os"
	"testing"
	"time"
)

func (se *SIPEngine) stateSnapshot() SIPCallState {
	se.mu.Lock()
	defer se.mu.Unlock()
	return se.callState
}

func TestPBXCall(t *testing.T) {
	user := os.Getenv("KISKEYA_TEST_USER")
	pass := os.Getenv("KISKEYA_TEST_PASS")
	domain := os.Getenv("KISKEYA_TEST_DOMAIN")
	target := os.Getenv("KISKEYA_TEST_TARGET")
	if user == "" || pass == "" || domain == "" || target == "" {
		t.Skip("set KISKEYA_TEST_USER/PASS/DOMAIN/TARGET to run the live PBX call test")
	}

	app := &App{
		emit: func(name string, data interface{}) {
			switch name {
			case "audio:mic_level", "audio:speaker_level":
				return // too chatty
			case "sip:log":
				fmt.Printf("\n%v", data)
			default:
				fmt.Printf("[event] %s: %v\n", name, data)
			}
		},
	}

	ae, err := NewAudioEngine(app)
	if err != nil {
		t.Skipf("audio engine unavailable in this environment: %v", err)
	}
	defer ae.Close()

	se, err := NewSIPEngine(app, ae)
	if err != nil {
		t.Fatalf("NewSIPEngine: %v", err)
	}
	se.account = &SIPAccount{Username: user, Password: pass, Domain: domain, Protocol: "udp"}
	if err := se.buildTransportUnlocked(0); err != nil {
		t.Fatalf("buildTransportUnlocked: %v", err)
	}
	defer se.Close()

	// Register first so the PBX routes our call.
	if _, perm := se.doRegister(120); perm || se.regState != StateRegistered {
		t.Fatalf("registration failed: state=%s err=%q", se.regState, se.regError)
	}
	t.Logf("registered as %s@%s", user, domain)

	// Place the call.
	t.Logf(">>> Dialing %s — PLEASE ANSWER on that extension <<<", target)
	if err := se.MakeCall(target); err != nil {
		t.Fatalf("MakeCall: %v", err)
	}

	// Wait up to 60s for the call to become active (you answering).
	deadline := time.Now().Add(60 * time.Second)
	last := se.stateSnapshot()
	t.Logf("state: %s", last)
	for time.Now().Before(deadline) {
		s := se.stateSnapshot()
		if s != last {
			t.Logf("state: %s -> %s", last, s)
			last = s
		}
		if s == StateActive {
			break
		}
		if s == StateIdle && last == StateIdle {
			// reset back to idle without ever connecting => call failed/declined
		}
		time.Sleep(200 * time.Millisecond)
	}

	if se.stateSnapshot() != StateActive {
		t.Fatalf("call did not reach active (final state: %s)", se.stateSnapshot())
	}
	t.Logf("✅ CALL ACTIVE — holding 8s (talk/listen now)")
	time.Sleep(8 * time.Second)

	// Hang up and confirm teardown.
	t.Logf(">>> Hanging up")
	if err := se.HangupCall(); err != nil {
		t.Fatalf("HangupCall: %v", err)
	}
	end := time.Now().Add(5 * time.Second)
	for time.Now().Before(end) {
		if se.stateSnapshot() == StateIdle {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if se.stateSnapshot() != StateIdle {
		t.Fatalf("call did not return to idle after hangup (state: %s)", se.stateSnapshot())
	}
	t.Logf("✅ Call ended cleanly (BYE sent, state idle)")
}
