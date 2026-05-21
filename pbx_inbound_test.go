//go:build pbxtest

// Live INBOUND-call integration test against a real PBX, using the real engine:
// register, wait for an inbound INVITE (a human must call the extension), auto-
// answer, confirm the call goes active, then hang up. Requires the PBX to have
// NAT handling enabled for the extension (force_rport/comedia) for the inbound
// ACK/media to complete through a home NAT.
//
//	KISKEYA_TEST_USER=1001 KISKEYA_TEST_PASS=… KISKEYA_TEST_DOMAIN=pbx.example.com \
//	  go test -tags pbxtest -run TestPBXInbound -v -timeout 180s
package main

import (
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestPBXInbound(t *testing.T) {
	user := os.Getenv("KISKEYA_TEST_USER")
	pass := os.Getenv("KISKEYA_TEST_PASS")
	domain := os.Getenv("KISKEYA_TEST_DOMAIN")
	if user == "" || pass == "" || domain == "" {
		t.Skip("set KISKEYA_TEST_USER/PASS/DOMAIN to run the live inbound test")
	}

	// Enable sipgo's transport/transaction debug logging to diagnose inbound.
	if os.Getenv("KISKEYA_DEBUG") != "" {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	}

	app := &App{emit: func(name string, data interface{}) {
		if name == "audio:mic_level" || name == "audio:speaker_level" {
			return
		}
		t.Logf("[event] %s: %v", name, data)
	}}

	ae, err := NewAudioEngine(app)
	if err != nil {
		t.Skipf("audio engine unavailable: %v", err)
	}
	defer ae.Close()

	se, err := NewSIPEngine(app, ae)
	if err != nil {
		t.Fatalf("NewSIPEngine: %v", err)
	}
	se.Register(&SIPAccount{Username: user, Password: pass, Domain: domain, Protocol: "udp"})
	defer se.Close()

	// Wait for registration.
	for i := 0; i < 50; i++ {
		se.mu.Lock()
		reg := se.regState
		se.mu.Unlock()
		if reg == StateRegistered {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	se.mu.Lock()
	reg := se.regState
	se.mu.Unlock()
	if reg != StateRegistered {
		t.Fatalf("not registered: %s", reg)
	}
	t.Logf("registered as %s@%s — call %s anytime in the next ~4.5 min...", user, domain, user)

	// Wait for an inbound INVITE (callState -> ringing).
	deadline := time.Now().Add(270 * time.Second)
	for time.Now().Before(deadline) {
		if se.stateSnapshot() == StateRinging {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if se.stateSnapshot() != StateRinging {
		t.Skip("no inbound call received within the window (skipping)")
	}

	t.Logf("inbound call ringing — auto-answering")
	if err := se.AnswerCall(); err != nil {
		t.Fatalf("AnswerCall: %v", err)
	}

	// Confirm it goes active.
	end := time.Now().Add(10 * time.Second)
	for time.Now().Before(end) {
		if se.stateSnapshot() == StateActive {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if se.stateSnapshot() != StateActive {
		t.Fatalf("inbound call did not reach active (state: %s)", se.stateSnapshot())
	}
	t.Logf("✅ INBOUND CALL ACTIVE — holding 5s")
	time.Sleep(5 * time.Second)

	if err := se.HangupCall(); err != nil {
		t.Fatalf("HangupCall: %v", err)
	}
	t.Logf("✅ hung up")
}
