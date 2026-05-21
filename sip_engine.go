package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/pion/sdp/v3"
)

// SIPAccount holds SIP account settings.
type SIPAccount struct {
	DisplayName      string `json:"displayName"`
	Username         string `json:"username"`
	Password         string `json:"password"`
	Domain           string `json:"domain"`
	Proxy            string `json:"proxy"`            // e.g. "sip.example.com:5060" or blank
	Protocol         string `json:"protocol"`         // "udp", "tcp" or "tls"
	STUNServer       string `json:"stunServer"`       // e.g. "stun.l.google.com:19302" or blank
	MediaEncryption  bool   `json:"mediaEncryption"`  // offer SRTP (SDES) for outgoing calls
	AllowInsecureTLS bool   `json:"allowInsecureTLS"` // skip TLS certificate verification (self-signed servers)
}

// SIPCallState defines the lifecycle states of a SIP session.
type SIPCallState string

const (
	StateIdle       SIPCallState = "idle"
	StateRegistering SIPCallState = "registering"
	StateRegistered SIPCallState = "registered"
	StateFailed     SIPCallState = "failed"
	StateDialing    SIPCallState = "dialing"
	StateRinging    SIPCallState = "ringing"
	StateActive     SIPCallState = "active"
)

// SIPEngine handles SIP registration and calling.
type SIPEngine struct {
	app         *App
	audioEngine *AudioEngine

	ua     *sipgo.UserAgent
	client *sipgo.Client
	server *sipgo.Server

	account     *SIPAccount
	regState    SIPCallState
	regError    string
	isRegistered int32 // atomic bool

	// Active Call State
	callState    SIPCallState
	remoteParty  string
	callStartTime time.Time
	isIncoming   bool
	callConnected bool   // true once the call reached StateActive (for accurate "answered")
	endReason    string // explicit end reason for call log ("rejected"/"declined"), else derived

	// SRTP key material negotiated for the active call (nil = plaintext RTP)
	callLocalSRTP  *SRTPKeyMaterial
	callRemoteSRTP *SRTPKeyMaterial

	// Dialog management via sipgo. The dialog API handles CSeq/tags/Route/
	// Record-Route, digest auth, ACK retransmission and CANCEL correctly.
	dialogClient  *sipgo.DialogClientCache
	dialogServer  *sipgo.DialogServerCache
	clientSession *sipgo.DialogClientSession // active outgoing call
	serverSession *sipgo.DialogServerSession // active incoming call
	callCancel    context.CancelFunc         // cancels an in-flight outgoing INVITE => CANCEL

	mu        sync.Mutex
	stopReg   chan struct{}
	stopKeepalive chan struct{} // stops the NAT keepalive goroutine
	localIP   string
	localSIPPort int
	allowInsecureTLS bool

	// Public address learned from the registrar's rport/received (the NAT mapping
	// of our signaling socket). Advertised in Contact so the PBX can reach us for
	// inbound calls behind a port-translating NAT.
	pubHost string
	pubPort int
}

func NewSIPEngine(app *App, ae *AudioEngine) (*SIPEngine, error) {
	se := &SIPEngine{
		app:         app,
		audioEngine: ae,
		regState:    StateIdle,
		callState:   StateIdle,
		localIP:     getLocalIP(),
	}

	return se, nil
}

// Close cleans up the SIP Engine. It unregisters synchronously (while the client
// is still alive) before tearing down the transport, to avoid racing StopServer.
func (se *SIPEngine) Close() {
	se.mu.Lock()
	if se.stopReg != nil {
		close(se.stopReg)
		se.stopReg = nil
	}
	wasRegistered := atomic.LoadInt32(&se.isRegistered) == 1
	se.mu.Unlock()

	if wasRegistered {
		atomic.StoreInt32(&se.isRegistered, 0)
		se.doRegister(0) // synchronous expire-0 REGISTER (no lock held)
	}

	se.StopServer()
}

// LogSIP traffic helper.
func (se *SIPEngine) LogSIP(direction string, msg string) {
	// Format direction header
	header := fmt.Sprintf("[%s] %s\n", direction, time.Now().Format("15:04:05.000"))
	se.app.EmitEvent("sip:log", header+msg+"\n----------------------------------------\n")
}

// StartServer binds a local SIP listener.
func (se *SIPEngine) StartServer(port int) error {
	se.mu.Lock()
	defer se.mu.Unlock()

	if se.server != nil {
		return nil
	}
	return se.buildTransportUnlocked(port)
}

// buildTransportUnlocked (re)creates the UserAgent, client and server and binds
// the local UDP/TCP listeners. It binds the sockets directly so the actual bound
// port is known (advertised in Contact/Via even when the preferred port is taken).
// The UA is always configured with a TLS config so "tls" transport works for
// outgoing REGISTER/INVITE. Caller must hold se.mu.
func (se *SIPEngine) buildTransportUnlocked(port int) error {
	// Stop any previous NAT keepalive before tearing down its socket.
	if se.stopKeepalive != nil {
		close(se.stopKeepalive)
		se.stopKeepalive = nil
	}
	// New socket => any previously learned NAT mapping is stale.
	se.pubHost = ""
	se.pubPort = 0

	// Tear down any existing transport so settings (TLS verification) re-apply.
	if se.server != nil {
		_ = se.server.Close()
		se.server = nil
	}

	// Bind the UDP listener FIRST so we know the local address. We bind IPv4
	// explicitly ("udp4"): on a dual-stack ([::]) socket the PBX's IPv4 source
	// arrives as an IPv4-mapped-IPv6 address, which mismatches sipgo's per-source
	// connection lookup and makes inbound server transactions fail to send their
	// responses. A pure-IPv4 socket keeps source/destination addresses consistent.
	// We also make the client send from this same socket (NAT pinhole alignment):
	// the REGISTER opens a pinhole on the listening port, which is exactly the port
	// our Contact advertises, so the registrar can route inbound calls back.
	udpAddr := fmt.Sprintf("0.0.0.0:%d", port)
	udpConn, err := net.ListenPacket("udp4", udpAddr)
	if err != nil {
		se.LogSIP("SYSTEM", fmt.Sprintf("UDP bind to %s failed (%v); falling back to a dynamic port", udpAddr, err))
		udpConn, err = net.ListenPacket("udp4", "0.0.0.0:0")
		if err != nil {
			return fmt.Errorf("failed to bind UDP socket: %v", err)
		}
	}
	se.localSIPPort = udpConn.LocalAddr().(*net.UDPAddr).Port
	udpLaddr := udpConn.LocalAddr().String()

	tlsCfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: se.allowInsecureTLS, //nolint:gosec // user-opt-in for self-signed SIP servers
	}

	ua, err := sipgo.NewUA(
		sipgo.WithUserAgent("Kiskeya Softphone"),
		sipgo.WithUserAgenTLSConfig(tlsCfg),
	)
	if err != nil {
		_ = udpConn.Close()
		return err
	}
	se.ua = ua

	// Client options: rport (RFC 3581) so the registrar learns our public address
	// for NAT. For UDP, bind outgoing requests to the listening socket so the
	// source port equals the Contact port (single NAT pinhole).
	clientOpts := []sipgo.ClientOption{sipgo.WithClientNAT()}
	proto := "udp"
	if se.account != nil {
		if p := strings.ToLower(se.account.Protocol); p != "" {
			proto = p
		}
	}
	if proto == "udp" {
		clientOpts = append(clientOpts, sipgo.WithClientConnectionAddr(udpLaddr))
	}
	client, err := sipgo.NewClient(ua, clientOpts...)
	if err != nil {
		_ = udpConn.Close()
		return err
	}
	se.client = client

	server, err := sipgo.NewServer(ua)
	if err != nil {
		_ = udpConn.Close()
		return err
	}
	se.server = server

	// Register dialog-based handlers. CANCEL needs no handler: ReadInvite wires
	// tx.OnCancel internally to end the dialog.
	se.server.OnInvite(se.onInvite)
	se.server.OnAck(se.onAck)
	se.server.OnBye(se.onBye)

	go func() { _ = se.server.ServeUDP(udpConn) }()

	// Bind TCP on the same (now known) port. TLS transport reuses the outbound
	// connection established during REGISTER, so no separate TLS listener/cert
	// is required for a client softphone.
	tcpLn, err := net.Listen("tcp4", fmt.Sprintf("0.0.0.0:%d", se.localSIPPort))
	if err == nil {
		go func() { _ = se.server.ServeTCP(tcpLn) }()
	} else {
		se.LogSIP("SYSTEM", fmt.Sprintf("TCP bind failed: %v (TCP/TLS inbound disabled)", err))
	}

	// Build dialog caches with a Contact pointing at our reachable address so
	// in-dialog requests (BYE/ACK) route back to us.
	contactHDR := se.buildContactHeader()
	se.dialogClient = sipgo.NewDialogClientCache(se.client, contactHDR)
	se.dialogServer = sipgo.NewDialogServerCache(se.client, contactHDR)

	// Start a NAT keepalive over UDP: a tiny CRLF ping to the registrar on the
	// same socket keeps the NAT pinhole open between registration refreshes, so
	// inbound INVITEs (and their ACKs) keep arriving. This is what production
	// softphones (e.g. Zoiper) do; without it inbound works only briefly after
	// each REGISTER, then the pinhole closes.
	if proto == "udp" && se.account != nil {
		target := se.account.Domain
		if se.account.Proxy != "" {
			target = se.account.Proxy
		}
		if !strings.Contains(target, ":") {
			target += ":5060"
		}
		if raddr, rerr := net.ResolveUDPAddr("udp", target); rerr == nil {
			se.stopKeepalive = make(chan struct{})
			go se.natKeepalive(udpConn, raddr, se.stopKeepalive)
		} else {
			se.LogSIP("SYSTEM", fmt.Sprintf("NAT keepalive disabled: cannot resolve %s: %v", target, rerr))
		}
	}

	se.LogSIP("SYSTEM", fmt.Sprintf("SIP transport ready on port %d (TLS cert verification: %v)", se.localSIPPort, !se.allowInsecureTLS))
	return nil
}

// natKeepalive sends a CRLF keepalive (RFC 5626 §3.5.1 / RFC 6223) to the
// registrar every ~15s on the signaling socket, keeping the NAT mapping open so
// the PBX can reach us for inbound calls. Runs until stop is closed or the socket
// is closed.
func (se *SIPEngine) natKeepalive(conn net.PacketConn, raddr *net.UDPAddr, stop chan struct{}) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	ping := []byte("\r\n\r\n")
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if _, err := conn.WriteTo(ping, raddr); err != nil {
				return // socket closed
			}
		}
	}
}

// buildContactHeader constructs the Contact used for dialogs, advertising our
// public/local IP, bound SIP port and transport.
func (se *SIPEngine) buildContactHeader() sip.ContactHeader {
	host, port, proto := se.contactAddr()
	user := "kiskeya"
	if se.account != nil && se.account.Username != "" {
		user = se.account.Username
	}
	contactStr := fmt.Sprintf("sip:%s@%s:%d", user, host, port)
	if proto != "udp" {
		contactStr += ";transport=" + proto
	}
	var u sip.Uri
	_ = sip.ParseUri(contactStr, &u)
	return sip.ContactHeader{Address: u}
}

// contactAddr returns the host, port and transport to advertise in Contact.
// It prefers the registrar-learned public mapping (received:rport) so inbound
// calls reach us through a port-translating NAT; otherwise it falls back to the
// STUN/local IP and the bound port.
func (se *SIPEngine) contactAddr() (host string, port int, proto string) {
	proto = "udp"
	host = se.localIP
	port = se.localSIPPort
	if se.account != nil {
		if p := strings.ToLower(se.account.Protocol); p != "" {
			proto = p
		}
		if se.pubHost == "" { // only STUN when we have no learned mapping
			host = se.resolvePublicIP(se.account.STUNServer)
		}
	}
	if se.pubHost != "" {
		host = se.pubHost
		if se.pubPort > 0 {
			port = se.pubPort
		}
	}
	return host, port, proto
}

// learnPublicMapping extracts our public NAT mapping from a REGISTER response's
// Via (received/rport) and, if it changed, updates the advertised Contact and
// rebuilds the dialog caches so inbound calls reach us. Returns true if changed.
func (se *SIPEngine) learnPublicMapping(res *sip.Response) bool {
	via := res.Via()
	if via == nil || via.Params == nil {
		return false
	}
	host, _ := via.Params.Get("received")
	if host == "" {
		return false
	}
	port := 0
	if rp, ok := via.Params.Get("rport"); ok && rp != "" {
		if p, err := strconv.Atoi(rp); err == nil {
			port = p
		}
	}

	se.mu.Lock()
	changed := host != se.pubHost || (port > 0 && port != se.pubPort)
	if !changed {
		se.mu.Unlock()
		return false
	}
	se.pubHost = host
	if port > 0 {
		se.pubPort = port
	}
	client := se.client
	// Rebuild dialog caches so inbound answers advertise the public Contact.
	if client != nil {
		c := se.buildContactHeader()
		se.dialogClient = sipgo.NewDialogClientCache(client, c)
		se.dialogServer = sipgo.NewDialogServerCache(client, c)
	}
	pubHost, pubPort := se.pubHost, se.pubPort
	se.mu.Unlock()

	se.LogSIP("SYSTEM", fmt.Sprintf("Public NAT mapping learned: %s:%d — re-advertising Contact", pubHost, pubPort))
	return true
}

// StopServer stops the local SIP listener.
func (se *SIPEngine) StopServer() {
	se.mu.Lock()
	defer se.mu.Unlock()

	if se.stopKeepalive != nil {
		close(se.stopKeepalive)
		se.stopKeepalive = nil
	}
	if se.server != nil {
		_ = se.server.Close()
		se.server = nil
	}
	se.ua = nil
	se.client = nil
}

// Register registers to the SIP account.
func (se *SIPEngine) Register(acc *SIPAccount) {
	se.mu.Lock()
	defer se.mu.Unlock()

	se.UnregisterUnlocked()

	se.account = acc
	se.allowInsecureTLS = acc.AllowInsecureTLS
	se.regState = StateRegistering
	se.regError = ""
	se.app.EmitEvent("sip:reg_state", map[string]interface{}{
		"state": string(StateRegistering),
		"error": "",
	})

	// (Re)build the transport so the selected protocol and TLS verification
	// setting take effect. Reuse the current bound port when we have one.
	port := se.localSIPPort
	if port == 0 {
		port = 5060
	}
	if err := se.buildTransportUnlocked(port); err != nil {
		se.regState = StateFailed
		se.regError = err.Error()
		se.app.EmitEvent("sip:reg_state", map[string]interface{}{
			"state": string(StateFailed),
			"error": err.Error(),
		})
		return
	}

	se.stopReg = make(chan struct{})

	go se.registrationLoop()
}

// Unregister performs unregistration.
func (se *SIPEngine) Unregister() {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.UnregisterUnlocked()
}

func (se *SIPEngine) UnregisterUnlocked() {
	if se.stopReg != nil {
		close(se.stopReg)
		se.stopReg = nil
	}

	if atomic.LoadInt32(&se.isRegistered) == 1 {
		atomic.StoreInt32(&se.isRegistered, 0)
		go se.doRegister(0) // Expire registration
	}

	se.regState = StateIdle
	se.regError = ""
	se.app.EmitEvent("sip:reg_state", map[string]interface{}{
		"state": string(StateIdle),
		"error": "",
	})
}

// registrationLoop keeps the account registered, refreshing at roughly half the
// server-granted lifetime. Transient failures back off exponentially (capped);
// permanent failures (bad credentials, forbidden) stop the loop so we don't
// hammer the registrar and trip fail2ban / lockouts.
func (se *SIPEngine) registrationLoop() {
	const (
		defaultExpires = 300
		minBackoff     = 2 * time.Second
		maxBackoff     = 5 * time.Minute
	)
	backoff := minBackoff

	timer := time.NewTimer(0) // fire immediately for the initial REGISTER
	defer timer.Stop()

	for {
		select {
		case <-se.stopReg:
			return
		case <-timer.C:
			next, permanent := se.doRegister(defaultExpires)
			switch {
			case permanent:
				// Bad credentials / forbidden: stop retrying until the user acts.
				se.LogSIP("SYSTEM", "Registration failed permanently; stopping auto-retry until settings change.")
				return
			case next <= 0:
				// Transient failure: exponential backoff.
				se.LogSIP("SYSTEM", fmt.Sprintf("Registration retry in %s", backoff))
				timer.Reset(backoff)
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			default:
				// Success: refresh well before expiry, reset backoff.
				backoff = minBackoff
				timer.Reset(next)
			}
		}
	}
}

// doRegister sends a single REGISTER. It returns the duration until the next
// refresh on success (>0), 0 for a transient failure (caller should back off),
// and permanent=true for a non-recoverable failure (caller should stop).
func (se *SIPEngine) doRegister(expires int) (next time.Duration, permanent bool) {
	// Snapshot account+client under the lock so a concurrent StopServer (which
	// nils se.client) can't cause a nil dereference here.
	se.mu.Lock()
	acc := se.account
	client := se.client
	se.mu.Unlock()
	if acc == nil || client == nil {
		return 0, true
	}

	targetDomain := acc.Domain
	if acc.Proxy != "" {
		targetDomain = acc.Proxy
	}

	recipientStr := fmt.Sprintf("sip:%s", targetDomain)
	if tp := transportParam(acc.Protocol); tp != "" {
		recipientStr += ";transport=" + tp
	}
	var recipientURI sip.Uri
	if err := sip.ParseUri(recipientStr, &recipientURI); err != nil {
		se.updateRegState(StateFailed, fmt.Sprintf("invalid server address: %v", err))
		return 0, true // a malformed server address won't fix itself by retrying
	}

	req, res, err := se.sendRegisterRequest(client, acc, recipientURI, expires)
	if err != nil {
		if expires > 0 {
			se.updateRegState(StateFailed, err.Error())
		}
		return 0, false // network/timeout: transient
	}

	// Authorization challenge. DoDigestAuth must receive the request that was
	// actually sent (it reuses its CSeq/headers), not a freshly built one.
	if res.StatusCode == 401 || res.StatusCode == 407 {
		se.LogSIP("SYSTEM", "Sending challenged REGISTER with authorization credentials...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		resAuth, aerr := client.DoDigestAuth(ctx, req, res, sipgo.DigestAuth{Username: acc.Username, Password: acc.Password})
		if aerr != nil {
			se.updateRegState(StateFailed, fmt.Sprintf("authentication failed: %v", aerr))
			return 0, false
		}
		se.LogSIP("Incoming (Auth Response)", resAuth.String())
		res = resAuth
	}

	switch {
	case res.StatusCode == sip.StatusOK:
		if expires <= 0 {
			atomic.StoreInt32(&se.isRegistered, 0)
			se.updateRegState(StateIdle, "")
			return 0, true // intentional unregister: stop the loop
		}
		// Learn our public NAT mapping from the response's Via (received/rport)
		// and re-advertise it so inbound calls reach us behind a port-translating NAT.
		mappingChanged := se.learnPublicMapping(res)
		atomic.StoreInt32(&se.isRegistered, 1)
		se.updateRegState(StateRegistered, "")
		if mappingChanged {
			// Re-register almost immediately so the registrar stores the corrected
			// (public) Contact instead of the stale one from this first attempt.
			return 1 * time.Second, false
		}
		granted := grantedExpires(res, expires)
		// Refresh at half the granted lifetime (min 15s of headroom).
		refresh := time.Duration(granted/2) * time.Second
		if refresh < 15*time.Second {
			refresh = 15 * time.Second
		}
		return refresh, false

	case res.StatusCode == 423: // Interval Too Brief
		min := headerInt(res, "Min-Expires", expires*2)
		se.LogSIP("SYSTEM", fmt.Sprintf("Server requires a longer registration interval; retrying with Expires=%d", min))
		return se.doRegister(min)

	case res.StatusCode == 401 || res.StatusCode == 407 || res.StatusCode == 403:
		// Credentials rejected even after auth (or forbidden): permanent.
		atomic.StoreInt32(&se.isRegistered, 0)
		se.updateRegState(StateFailed, fmt.Sprintf("authentication rejected: %d %s", res.StatusCode, res.Reason))
		return 0, true

	case res.StatusCode == 404:
		se.updateRegState(StateFailed, fmt.Sprintf("account not found: %d %s", res.StatusCode, res.Reason))
		return 0, true

	default:
		// 5xx and everything else: transient, let the caller back off.
		se.updateRegState(StateFailed, fmt.Sprintf("registration failed: %d %s", res.StatusCode, res.Reason))
		return 0, false
	}
}

// buildRegister constructs a REGISTER request for the account.
func (se *SIPEngine) buildRegister(acc *SIPAccount, recipientURI sip.Uri, expires int) (*sip.Request, error) {
	var fromURI sip.Uri
	if err := sip.ParseUri(fmt.Sprintf("sip:%s@%s", acc.Username, acc.Domain), &fromURI); err != nil {
		return nil, fmt.Errorf("invalid account URI: %v", err)
	}

	// Advertise the registrar-learned public mapping (received:rport) when known,
	// so the PBX can reach us for inbound calls behind a port-translating NAT.
	contactHost, contactPort, _ := se.contactAddr()
	if contactPort == 0 {
		contactPort = 5060
	}
	contactStr := fmt.Sprintf("sip:%s@%s:%d", acc.Username, contactHost, contactPort)
	if tp := transportParam(acc.Protocol); tp != "" {
		contactStr += ";transport=" + tp
	}
	var contactURI sip.Uri
	_ = sip.ParseUri(contactStr, &contactURI)

	req := sip.NewRequest(sip.REGISTER, recipientURI)
	req.AppendHeader(sip.NewHeader("From", fmt.Sprintf("<%s>", fromURI.String())))
	req.AppendHeader(sip.NewHeader("To", fmt.Sprintf("<%s>", fromURI.String())))
	req.AppendHeader(sip.NewHeader("Contact", fmt.Sprintf("<%s>", contactURI.String())))
	req.AppendHeader(sip.NewHeader("Expires", fmt.Sprintf("%d", expires)))
	req.SetBody([]byte{})
	se.applyTransport(req)
	return req, nil
}

// sendRegisterRequest sends a REGISTER and returns the sent request (now carrying
// the CSeq/Via the client populated, needed for digest auth) and the response.
func (se *SIPEngine) sendRegisterRequest(client *sipgo.Client, acc *SIPAccount, recipientURI sip.Uri, expires int) (*sip.Request, *sip.Response, error) {
	req, err := se.buildRegister(acc, recipientURI, expires)
	if err != nil {
		return nil, nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	se.LogSIP("Outgoing (REGISTER)", req.String())
	tx, err := client.TransactionRequest(ctx, req)
	if err != nil {
		return nil, nil, fmt.Errorf("network error: %v", err)
	}
	defer tx.Terminate()

	select {
	case r := <-tx.Responses():
		se.LogSIP("Incoming", r.String())
		return req, r, nil
	case <-ctx.Done():
		return nil, nil, fmt.Errorf("connection timeout")
	}
}

// grantedExpires extracts the lifetime the registrar granted, from the Expires
// header (falling back to the requested value).
func grantedExpires(res *sip.Response, requested int) int {
	if v := headerInt(res, "Expires", 0); v > 0 {
		return v
	}
	return requested
}

// headerInt parses a numeric header value, returning def if absent/unparseable.
func headerInt(res *sip.Response, name string, def int) int {
	h := res.GetHeader(name)
	if h == nil {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(h.Value()))
	if err != nil {
		return def
	}
	return n
}

func (se *SIPEngine) updateRegState(state SIPCallState, errStr string) {
	se.mu.Lock()
	se.regState = state
	se.regError = errStr
	se.mu.Unlock()

	se.app.EmitEvent("sip:reg_state", map[string]interface{}{
		"state": string(state),
		"error": errStr,
	})
}

// MakeCall starts an outgoing call using the sipgo dialog client, which manages
// CSeq, dialog tags, Route/Record-Route, digest auth, ACK and CANCEL correctly.
func (se *SIPEngine) MakeCall(destination string) error {
	se.mu.Lock()
	defer se.mu.Unlock()

	if se.callState != StateIdle {
		return fmt.Errorf("call already in progress")
	}
	if se.account == nil {
		return fmt.Errorf("SIP account not registered")
	}
	if se.dialogClient == nil {
		return fmt.Errorf("SIP transport not ready")
	}

	// Resolve destination into a request URI.
	var destUser, destHost string
	if strings.Contains(destination, "@") {
		parts := strings.SplitN(destination, "@", 2)
		destUser, destHost = parts[0], parts[1]
	} else {
		destUser, destHost = destination, se.account.Domain
	}
	recipientStr := fmt.Sprintf("sip:%s@%s", destUser, destHost)
	if tp := transportParam(se.account.Protocol); tp != "" {
		recipientStr += ";transport=" + tp
	}
	var recipientURI sip.Uri
	if err := sip.ParseUri(recipientStr, &recipientURI); err != nil {
		return fmt.Errorf("invalid destination: %v", err)
	}

	// Allocate media + optional SRTP, build the SDP offer.
	rtpPort, err := se.audioEngine.AllocateRTPPort()
	if err != nil {
		return fmt.Errorf("audio port allocation failed: %v", err)
	}
	var localSRTP *SRTPKeyMaterial
	if se.account.MediaEncryption {
		if localSRTP, err = GenerateSRTPKey(); err != nil {
			return fmt.Errorf("failed to generate SRTP key: %v", err)
		}
	}
	se.callLocalSRTP = localSRTP
	sdpOffer := se.createSDPOffer(rtpPort, localSRTP)

	se.callState = StateDialing
	se.remoteParty = destUser
	se.isIncoming = false
	se.callConnected = false
	se.endReason = ""
	se.app.EmitEvent("sip:call_state", map[string]interface{}{
		"state":       string(StateDialing),
		"remoteParty": destUser,
		"incoming":    false,
	})

	dc := se.dialogClient
	acc := se.account
	go se.runOutgoingCall(dc, recipientURI, sdpOffer, destUser, acc)
	return nil
}

// runOutgoingCall drives an outgoing INVITE to completion off the UI goroutine.
func (se *SIPEngine) runOutgoingCall(dc *sipgo.DialogClientCache, recipient sip.Uri, sdpOffer []byte, destUser string, acc *SIPAccount) {
	// A cancelable context lets HangupCall-before-answer trigger a CANCEL.
	ctx, cancel := context.WithCancel(context.Background())
	se.mu.Lock()
	se.callCancel = cancel
	se.mu.Unlock()
	defer cancel()

	// Build an explicit From for our identity. Without it, sipgo derives From from
	// the UA name ("Kiskeya Softphone"), producing a malformed/incorrect caller.
	fromParams := sip.NewParams()
	fromParams.Add("tag", sip.GenerateTagN(16))
	fromHdr := &sip.FromHeader{
		DisplayName: acc.DisplayName,
		Address:     sip.Uri{Scheme: "sip", User: acc.Username, Host: acc.Domain},
		Params:      fromParams,
	}

	se.LogSIP("Outgoing (INVITE)", fmt.Sprintf("INVITE %s", recipient.String()))
	session, err := dc.Invite(ctx, recipient, sdpOffer, fromHdr, sip.NewHeader("Content-Type", "application/sdp"))
	if err != nil {
		se.LogSIP("SYSTEM", fmt.Sprintf("INVITE failed: %v", err))
		se.resetCallState()
		return
	}
	se.mu.Lock()
	se.clientSession = session
	se.mu.Unlock()

	// WaitAnswer handles provisional responses, digest auth and CANCEL-on-cancel.
	err = session.WaitAnswer(ctx, sipgo.AnswerOptions{
		Username: acc.Username,
		Password: acc.Password,
		OnResponse: func(res *sip.Response) error {
			if res.StatusCode == 180 || res.StatusCode == 183 {
				se.setCallStateRinging(destUser)
			}
			return nil
		},
	})
	if err != nil {
		se.LogSIP("SYSTEM", fmt.Sprintf("Call not established: %v", err))
		se.resetCallState()
		return
	}

	// 2xx received — negotiate media, ACK, start audio.
	resp := session.InviteResponse
	remoteIP, remotePort, codec, remoteSRTP, perr := se.parseSDP(resp.Body())
	if perr != nil {
		se.LogSIP("SYSTEM", fmt.Sprintf("SDP negotiation failed: %v", perr))
		_ = session.Ack(context.Background())
		_ = session.Bye(context.Background())
		se.resetCallState()
		return
	}
	if err := session.Ack(ctx); err != nil {
		se.LogSIP("SYSTEM", fmt.Sprintf("ACK failed: %v", err))
	}

	se.mu.Lock()
	localSRTP, secure := se.negotiateSRTP(remoteSRTP)
	se.mu.Unlock()

	if err := se.audioEngine.StartCall(remoteIP, remotePort, codec, localSRTP, remoteSRTP); err != nil {
		se.LogSIP("SYSTEM", fmt.Sprintf("Audio startup failed: %v", err))
		_ = session.Bye(context.Background())
		se.resetCallState()
		return
	}
	se.LogSIP("SYSTEM", fmt.Sprintf("Media established (codec %s, SRTP %v)", codec, secure))

	se.mu.Lock()
	se.callState = StateActive
	se.callConnected = true
	se.callStartTime = time.Now()
	se.mu.Unlock()
	se.app.EmitEvent("sip:call_state", map[string]interface{}{
		"state":       string(StateActive),
		"remoteParty": destUser,
		"incoming":    false,
		"codec":       codec,
		"secure":      secure,
	})

	// Tear down locally when the dialog ends (remote BYE).
	go se.watchDialogEnd(session.Context())
}

// setCallStateRinging transitions an outgoing call to ringing on a provisional.
func (se *SIPEngine) setCallStateRinging(destUser string) {
	se.mu.Lock()
	if se.callState == StateDialing {
		se.callState = StateRinging
	}
	se.mu.Unlock()
	se.app.EmitEvent("sip:call_state", map[string]interface{}{
		"state":       string(StateRinging),
		"remoteParty": destUser,
		"incoming":    false,
	})
}

// AnswerCall answers a ringing incoming call via the dialog server session.
func (se *SIPEngine) AnswerCall() error {
	se.mu.Lock()
	if se.callState != StateRinging || !se.isIncoming {
		se.mu.Unlock()
		return fmt.Errorf("no incoming call to answer")
	}
	dtx := se.serverSession
	se.mu.Unlock()
	if dtx == nil {
		return fmt.Errorf("missing dialog session")
	}

	offer := dtx.InviteRequest.Body()
	remoteIP, remotePort, codec, remoteSRTP, err := se.parseSDP(offer)
	if err != nil {
		go func() { _ = dtx.Respond(488, "Not Acceptable Here", nil) }()
		se.resetCallState()
		return fmt.Errorf("SDP negotiation failed: %v", err)
	}

	localPort, err := se.audioEngine.AllocateRTPPort()
	if err != nil {
		go func() { _ = dtx.Respond(500, "Server Internal Error", nil) }()
		se.resetCallState()
		return fmt.Errorf("audio port allocation failed: %v", err)
	}

	var localSRTP *SRTPKeyMaterial
	if remoteSRTP != nil {
		if localSRTP, err = GenerateSRTPKey(); err != nil {
			go func() { _ = dtx.Respond(500, "Server Internal Error", nil) }()
			se.resetCallState()
			return fmt.Errorf("failed to generate SRTP key: %v", err)
		}
	}
	secure := localSRTP != nil && remoteSRTP != nil
	sdpAnswer := se.createSDPAnswer(localPort, codec, localSRTP, offerHasDTMF(offer))

	if err := se.audioEngine.StartCall(remoteIP, remotePort, codec, localSRTP, remoteSRTP); err != nil {
		go func() { _ = dtx.Respond(500, "Server Internal Error", nil) }()
		se.resetCallState()
		return fmt.Errorf("failed to start audio: %v", err)
	}

	se.mu.Lock()
	se.callLocalSRTP = localSRTP
	se.callRemoteSRTP = remoteSRTP
	se.callState = StateActive
	se.callConnected = true
	se.callStartTime = time.Now()
	remote := se.remoteParty
	se.mu.Unlock()

	se.LogSIP("SYSTEM", fmt.Sprintf("Media established (codec %s, SRTP %v)", codec, secure))
	se.app.EmitEvent("sip:call_state", map[string]interface{}{
		"state":       string(StateActive),
		"remoteParty": remote,
		"incoming":    true,
		"codec":       codec,
		"secure":      secure,
	})

	// RespondSDP blocks until the ACK is received, so run it off this goroutine.
	go func() {
		if err := dtx.RespondSDP(sdpAnswer); err != nil {
			se.LogSIP("SYSTEM", fmt.Sprintf("200 OK (SDP) failed: %v", err))
		}
	}()
	return nil
}

// RejectCall declines a ringing incoming call with 486 Busy Here.
func (se *SIPEngine) RejectCall() error {
	se.mu.Lock()
	if se.callState != StateRinging || !se.isIncoming {
		se.mu.Unlock()
		return fmt.Errorf("no incoming call to reject")
	}
	dtx := se.serverSession
	se.endReason = "rejected"
	se.mu.Unlock()

	if dtx != nil {
		go func() { _ = dtx.Respond(486, "Busy Here", nil) }()
	}
	se.resetCallState()
	return nil
}

// HangupCall ends whatever call is in progress (outgoing or incoming).
func (se *SIPEngine) HangupCall() error {
	se.mu.Lock()
	state := se.callState
	if state == StateIdle {
		se.mu.Unlock()
		return nil
	}
	incoming := se.isIncoming
	cs := se.clientSession
	ss := se.serverSession
	cancel := se.callCancel
	if state == StateRinging && incoming {
		se.endReason = "declined"
	}
	se.mu.Unlock()

	se.audioEngine.StopCall()

	switch {
	case !incoming && (state == StateDialing || state == StateRinging):
		// Outgoing, not yet answered: cancel the INVITE (WaitAnswer sends CANCEL).
		if cancel != nil {
			cancel()
		}
	case incoming && state == StateRinging && ss != nil:
		go func() { _ = ss.Respond(486, "Busy Here", nil) }()
	case state == StateActive && !incoming && cs != nil:
		go func() { _ = cs.Bye(context.Background()) }()
	case state == StateActive && incoming && ss != nil:
		go func() { _ = ss.Bye(context.Background()) }()
	}

	se.resetCallState()
	return nil
}

// onInvite handles inbound INVITEs via the dialog server.
func (se *SIPEngine) onInvite(req *sip.Request, tx sip.ServerTransaction) {
	se.LogSIP("Incoming (INVITE)", req.String())

	if se.dialogServer == nil {
		_ = tx.Respond(sip.NewResponseFromRequest(req, 503, "Service Unavailable", nil))
		return
	}
	dtx, err := se.dialogServer.ReadInvite(req, tx)
	if err != nil {
		se.LogSIP("SYSTEM", fmt.Sprintf("ReadInvite failed: %v", err))
		return
	}

	se.mu.Lock()
	if se.callState != StateIdle {
		se.mu.Unlock()
		go func() { _ = dtx.Respond(486, "Busy Here", nil) }()
		return
	}

	caller := "Unknown"
	if from := req.From(); from != nil {
		caller = from.Address.User
		if from.DisplayName != "" {
			caller = fmt.Sprintf("%s (%s)", from.DisplayName, from.Address.User)
		}
	}
	se.serverSession = dtx
	se.callState = StateRinging
	se.isIncoming = true
	se.remoteParty = caller
	se.callConnected = false
	se.endReason = ""
	se.mu.Unlock()

	// Send 180 Ringing (provisional, non-blocking).
	if err := dtx.Respond(180, "Ringing", nil); err != nil {
		se.LogSIP("SYSTEM", fmt.Sprintf("180 Ringing send failed: %v", err))
	}

	se.app.EmitEvent("sip:call_state", map[string]interface{}{
		"state":       string(StateRinging),
		"remoteParty": caller,
		"incoming":    true,
	})

	// CRITICAL: sipgo's Server terminates the INVITE transaction the moment this
	// handler returns unless a *final* response was already sent. A softphone
	// answers asynchronously (the user clicks answer, AnswerCall sends the 200),
	// so we must keep this handler alive for the whole call by blocking until the
	// dialog ends (answered→BYE, rejected, or canceled). Then clean up.
	<-dtx.Context().Done()
	se.audioEngine.StopCall()
	se.resetCallState()
}

// onAck confirms the dialog for an answered incoming call.
func (se *SIPEngine) onAck(req *sip.Request, tx sip.ServerTransaction) {
	if se.dialogServer == nil {
		return
	}
	if err := se.dialogServer.ReadAck(req, tx); err != nil {
		se.LogSIP("SYSTEM", fmt.Sprintf("ReadAck failed: %v", err))
	}
}

// onBye routes a BYE to the matching dialog (incoming or outgoing), which
// responds 200 and ends the dialog (triggering local teardown via watchDialogEnd).
func (se *SIPEngine) onBye(req *sip.Request, tx sip.ServerTransaction) {
	se.LogSIP("Incoming (BYE)", req.String())
	if se.dialogServer != nil {
		if err := se.dialogServer.ReadBye(req, tx); err == nil {
			return
		}
	}
	if se.dialogClient != nil {
		if err := se.dialogClient.ReadBye(req, tx); err == nil {
			return
		}
	}
	se.LogSIP("SYSTEM", "BYE for unknown dialog")
}

// watchDialogEnd stops audio and resets call state when a dialog's context is
// cancelled (remote BYE, CANCEL, or our own teardown). resetCallState is idempotent.
func (se *SIPEngine) watchDialogEnd(ctx context.Context) {
	if ctx == nil {
		return
	}
	<-ctx.Done()
	se.LogSIP("SYSTEM", fmt.Sprintf("dialog ended (cause: %v)", context.Cause(ctx)))
	se.audioEngine.StopCall()
	se.resetCallState()
}

func (se *SIPEngine) resetCallState() {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.resetCallStateUnlocked()
}

func (se *SIPEngine) resetCallStateUnlocked() {
	// Idempotent: a redundant reset (e.g. local hangup racing a remote BYE) must
	// not emit a second idle event and double-log the call.
	if se.callState == StateIdle {
		return
	}

	// Compute call-log details from the live state BEFORE clearing it, and ship
	// them inside the idle event so the App can persist an accurate history entry.
	duration := 0
	if !se.callStartTime.IsZero() {
		duration = int(time.Since(se.callStartTime).Seconds())
	}
	number := se.remoteParty
	direction := "outgoing"
	if se.isIncoming {
		direction = "incoming"
	}
	// "answered" is driven by whether the call actually connected, not by a
	// non-zero whole-second duration (sub-second calls were misclassified).
	status := "answered"
	if !se.callConnected {
		switch {
		case se.endReason != "":
			status = se.endReason
		case se.isIncoming:
			status = "missed"
		default:
			status = "failed"
		}
	}

	se.callState = StateIdle
	se.remoteParty = ""
	se.isIncoming = false
	se.callConnected = false
	se.callStartTime = time.Time{}
	se.endReason = ""
	se.callLocalSRTP = nil
	se.callRemoteSRTP = nil
	se.clientSession = nil
	se.serverSession = nil
	se.callCancel = nil

	se.app.EmitEvent("sip:call_state", map[string]interface{}{
		"state":        string(StateIdle),
		"remoteParty":  "",
		"incoming":     false,
		"logNumber":    number,
		"logDirection": direction,
		"logStatus":    status,
		"logDuration":  duration,
	})
}

// SDP Helpers

// mediaProfile returns the RTP profile tokens for the media line; SAVP when SRTP
// key material is supplied, otherwise plain AVP.
func mediaProfile(srtpKey *SRTPKeyMaterial) []string {
	if srtpKey != nil {
		return []string{"RTP", "SAVP"}
	}
	return []string{"RTP", "AVP"}
}

func (se *SIPEngine) createSDPOffer(rtpPort int, srtpKey *SRTPKeyMaterial) []byte {
	stunServer := ""
	if se.account != nil {
		stunServer = se.account.STUNServer
	}
	contactIP := se.resolvePublicIP(stunServer)

	dtmfPT := fmt.Sprintf("%d", dtmfPayloadType)
	attrs := []sdp.Attribute{
		{Key: "rtpmap", Value: "0 PCMU/8000"},
		{Key: "rtpmap", Value: "8 PCMA/8000"},
		{Key: "rtpmap", Value: dtmfPT + " telephone-event/8000"},
		{Key: "fmtp", Value: dtmfPT + " 0-16"},
	}
	if srtpKey != nil {
		attrs = append(attrs, sdp.Attribute{Key: "crypto", Value: srtpKey.CryptoAttribute(1)})
	}
	attrs = append(attrs, sdp.Attribute{Key: "sendrecv"})

	session := sdp.SessionDescription{
		Version: 0,
		Origin: sdp.Origin{
			Username:       "-",
			SessionID:      uint64(rand.Int63n(1000000)),
			SessionVersion: 1,
			NetworkType:    "IN",
			AddressType:    "IP4",
			UnicastAddress: contactIP,
		},
		SessionName: "Kiskeya",
		ConnectionInformation: &sdp.ConnectionInformation{
			NetworkType: "IN",
			AddressType: "IP4",
			Address:     &sdp.Address{Address: contactIP},
		},
		TimeDescriptions: []sdp.TimeDescription{
			{
				Timing: sdp.Timing{
					StartTime: 0,
					StopTime:  0,
				},
			},
		},
		MediaDescriptions: []*sdp.MediaDescription{
			{
				MediaName: sdp.MediaName{
					Media:   "audio",
					Port:    sdp.RangedPort{Value: rtpPort},
					Protos:  mediaProfile(srtpKey),
					Formats: []string{"0", "8", dtmfPT}, // PCMU, PCMA, telephone-event
				},
				Attributes: attrs,
			},
		},
	}

	data, _ := session.Marshal()
	return data
}

func (se *SIPEngine) createSDPAnswer(rtpPort int, codec string, srtpKey *SRTPKeyMaterial, withDTMF bool) []byte {
	format := "0"
	rtpmap := "0 PCMU/8000"
	if codec == "PCMA" {
		format = "8"
		rtpmap = "8 PCMA/8000"
	}

	stunServer := ""
	if se.account != nil {
		stunServer = se.account.STUNServer
	}
	contactIP := se.resolvePublicIP(stunServer)

	formats := []string{format}
	attrs := []sdp.Attribute{
		{Key: "rtpmap", Value: rtpmap},
	}
	// Only echo telephone-event if the caller offered it (RFC 3264: an answer
	// must not introduce formats absent from the offer).
	if withDTMF {
		dtmfPT := fmt.Sprintf("%d", dtmfPayloadType)
		formats = append(formats, dtmfPT)
		attrs = append(attrs,
			sdp.Attribute{Key: "rtpmap", Value: dtmfPT + " telephone-event/8000"},
			sdp.Attribute{Key: "fmtp", Value: dtmfPT + " 0-16"},
		)
	}
	if srtpKey != nil {
		attrs = append(attrs, sdp.Attribute{Key: "crypto", Value: srtpKey.CryptoAttribute(1)})
	}
	attrs = append(attrs, sdp.Attribute{Key: "sendrecv"})

	session := sdp.SessionDescription{
		Version: 0,
		Origin: sdp.Origin{
			Username:       "-",
			SessionID:      uint64(rand.Int63n(1000000)),
			SessionVersion: 1,
			NetworkType:    "IN",
			AddressType:    "IP4",
			UnicastAddress: contactIP,
		},
		SessionName: "Kiskeya",
		ConnectionInformation: &sdp.ConnectionInformation{
			NetworkType: "IN",
			AddressType: "IP4",
			Address:     &sdp.Address{Address: contactIP},
		},
		TimeDescriptions: []sdp.TimeDescription{
			{
				Timing: sdp.Timing{
					StartTime: 0,
					StopTime:  0,
				},
			},
		},
		MediaDescriptions: []*sdp.MediaDescription{
			{
				MediaName: sdp.MediaName{
					Media:   "audio",
					Port:    sdp.RangedPort{Value: rtpPort},
					Protos:  mediaProfile(srtpKey),
					Formats: formats,
				},
				Attributes: attrs,
			},
		},
	}

	data, _ := session.Marshal()
	return data
}

// offerHasDTMF reports whether the remote SDP advertises RFC 4733 telephone-event,
// so our answer can echo it (and so DTMF can be sent in-band).
func offerHasDTMF(rawSDP []byte) bool {
	return strings.Contains(strings.ToLower(string(rawSDP)), "telephone-event")
}

// parseSDP extracts the remote media endpoint and negotiated codec. If the media
// line is SAVP and carries a usable a=crypto attribute, the remote SRTP key is
// returned too (nil otherwise, meaning plaintext RTP).
func (se *SIPEngine) parseSDP(rawSDP []byte) (string, int, string, *SRTPKeyMaterial, error) {
	sd := &sdp.SessionDescription{}
	if err := sd.Unmarshal(rawSDP); err != nil {
		return "", 0, "", nil, err
	}

	remoteIP := ""
	if sd.ConnectionInformation != nil && sd.ConnectionInformation.Address != nil {
		remoteIP = sd.ConnectionInformation.Address.Address
	}

	remotePort := 0
	codec := "PCMU"
	var remoteSRTP *SRTPKeyMaterial

	for _, md := range sd.MediaDescriptions {
		if md.MediaName.Media == "audio" {
			remotePort = md.MediaName.Port.Value
			if md.ConnectionInformation != nil && md.ConnectionInformation.Address != nil {
				remoteIP = md.ConnectionInformation.Address.Address
			}

			// Negotiate codec (prefer PCMU, then PCMA)
			hasPCMU := false
			hasPCMA := false
			for _, fmtVal := range md.MediaName.Formats {
				if fmtVal == "0" {
					hasPCMU = true
				} else if fmtVal == "8" {
					hasPCMA = true
				}
			}

			if hasPCMU {
				codec = "PCMU"
			} else if hasPCMA {
				codec = "PCMA"
			} else {
				return "", 0, "", nil, fmt.Errorf("no supported codecs offered (PCMU/PCMA required)")
			}

			// Extract SRTP key material from the first usable crypto attribute.
			for _, attr := range md.Attributes {
				if attr.Key == "crypto" {
					if km, err := ParseCryptoAttribute(attr.Value); err == nil {
						remoteSRTP = km
						break
					}
				}
			}
			break
		}
	}

	if remoteIP == "" || remotePort == 0 {
		return "", 0, "", nil, fmt.Errorf("failed to extract valid RTP Connection Information")
	}

	return remoteIP, remotePort, codec, remoteSRTP, nil
}

// transportParam returns the value to use in a ";transport=" URI parameter for
// the given protocol, or "" for UDP (the default) to preserve legacy behaviour.
func transportParam(protocol string) string {
	switch strings.ToLower(protocol) {
	case "tcp":
		return "tcp"
	case "tls":
		return "tls"
	default:
		return ""
	}
}

// negotiateSRTP decides whether SRTP is active for an outgoing call: it requires
// both that we offered a local key and that the remote answered with one.
// Returns the local key to use (nil when plaintext) and whether media is secure.
func (se *SIPEngine) negotiateSRTP(remoteSRTP *SRTPKeyMaterial) (*SRTPKeyMaterial, bool) {
	if se.callLocalSRTP != nil && remoteSRTP != nil {
		se.callRemoteSRTP = remoteSRTP
		return se.callLocalSRTP, true
	}
	// Remote declined (or we did not offer) SRTP: fall back to plaintext.
	se.callLocalSRTP = nil
	se.callRemoteSRTP = nil
	return nil, false
}

// applyTransport forces an outgoing request onto the account's transport for
// non-UDP protocols so sipgo dials TCP/TLS and stamps the correct Via.
func (se *SIPEngine) applyTransport(req *sip.Request) {
	if se.account == nil {
		return
	}
	if tp := transportParam(se.account.Protocol); tp != "" {
		req.SetTransport(strings.ToUpper(tp))
	}
}

// Utility Helpers
func getLocalIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

func (se *SIPEngine) resolvePublicIP(stunServer string) string {
	if stunServer == "" {
		return se.localIP
	}
	se.LogSIP("SYSTEM", fmt.Sprintf("Querying STUN server %s for public IP...", stunServer))
	publicIP, err := GetPublicIP(stunServer)
	if err == nil && publicIP != "" {
		se.LogSIP("SYSTEM", fmt.Sprintf("STUN successfully resolved public IP: %s", publicIP))
		return publicIP
	}
	se.LogSIP("SYSTEM", fmt.Sprintf("STUN resolution failed: %v. Falling back to local IP.", err))
	return se.localIP
}
