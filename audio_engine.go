package main

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gen2brain/malgo"
	"github.com/pion/rtp"
	"github.com/pion/srtp/v3"
	"github.com/zaf/g711"
)

// Audio framing constants. Media is G.711 (PCMU/PCMA) @ 8kHz S16 mono, 20ms
// packets: 160 samples per frame => 320 bytes of S16 PCM, 160 bytes encoded.
const (
	audioSampleRate = 8000
	frameSamples    = 160 // 20ms @ 8kHz
	frameBytes      = frameSamples * 2

	// Jitter buffer playout depth and cap.
	jbTargetFrames = 3  // ~60ms of pre-roll before playout starts
	jbMaxFrames    = 10 // ~200ms hard cap; oldest frame is dropped on overflow

	// Level event emission rate (Hz) for UI meters, off the audio thread.
	levelEmitHz = 14
)

// AudioDevice represents a system microphone or speaker.
type AudioDevice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// jitterBuffer is an adaptive, reordering jitter buffer for the receive/playback
// path. RTP packets arrive on rtpReceiveLoop (a goroutine) and are inserted by
// sequence number; the realtime playback callback pulls whole 20ms frames in
// order. Out-of-order packets are reordered, duplicates dropped, and missing
// frames concealed (PLC) by repeating the last decoded frame at decaying
// amplitude instead of injecting silence.
//
// All access is guarded by mu, which is held only for short, allocation-free
// critical sections so the audio thread never blocks meaningfully.
type jitterBuffer struct {
	mu sync.Mutex

	// frames holds decoded S16 PCM keyed by 16-bit RTP sequence number. Each
	// entry is exactly frameBytes long.
	frames map[uint16][]byte

	started  bool   // true once we have buffered jbTargetFrames and begun playout
	nextSeq  uint16 // sequence number of the frame to play next
	haveNext bool   // whether nextSeq has been initialised

	// lastFrame is the most recently emitted PCM frame, reused for PLC.
	lastFrame []byte
	// concealCount counts consecutive concealed frames, used to decay PLC gain.
	concealCount int
}

func newJitterBuffer() *jitterBuffer {
	return &jitterBuffer{
		frames:    make(map[uint16][]byte, jbMaxFrames+4),
		lastFrame: make([]byte, frameBytes),
	}
}

// seqLess reports whether a is "before" b in RTP sequence space (RFC 1982
// serial-number arithmetic over 16 bits).
func seqLess(a, b uint16) bool {
	return (b-a)&0x8000 == 0 && a != b
}

// Reset clears all buffered state for a new call.
func (jb *jitterBuffer) Reset() {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	for k := range jb.frames {
		delete(jb.frames, k)
	}
	jb.started = false
	jb.haveNext = false
	jb.concealCount = 0
	// Zero the PLC reference so a stale frame from a previous call can't leak.
	for i := range jb.lastFrame {
		jb.lastFrame[i] = 0
	}
}

// Insert places a decoded PCM frame at the given sequence number. Duplicates and
// frames older than what we've already played are dropped. On overflow the oldest
// buffered frame is evicted (whole-frame, never a byte slice).
func (jb *jitterBuffer) Insert(seq uint16, pcm []byte) {
	if len(pcm) == 0 {
		return
	}

	jb.mu.Lock()
	defer jb.mu.Unlock()

	// Drop frames we've already advanced past (late arrivals).
	if jb.started && jb.haveNext && seqLess(seq, jb.nextSeq) {
		return
	}
	// Drop duplicates.
	if _, exists := jb.frames[seq]; exists {
		return
	}

	// Normalize to exactly frameBytes (a fixed 20ms frame) so the playback path
	// always emits a clean, full frame regardless of the sender's ptime: a
	// shorter payload is zero-padded, a longer one is truncated. This keeps the
	// realtime Pop path branch-free and guarantees no stale-tail bytes.
	buf := make([]byte, frameBytes)
	copy(buf, pcm)
	jb.frames[seq] = buf

	// Overflow: cap total buffered audio. Evict the oldest whole frame(s).
	for len(jb.frames) > jbMaxFrames {
		oldest, ok := jb.oldestSeqLocked()
		if !ok {
			break
		}
		delete(jb.frames, oldest)
		// If we evicted the frame we were about to play, resync nextSeq forward.
		if jb.started && jb.haveNext && oldest == jb.nextSeq {
			jb.nextSeq++
		}
	}
}

// oldestSeqLocked returns the smallest (in serial-number order) buffered sequence
// number. Caller must hold mu.
func (jb *jitterBuffer) oldestSeqLocked() (uint16, bool) {
	var oldest uint16
	found := false
	for s := range jb.frames {
		if !found || seqLess(s, oldest) {
			oldest = s
			found = true
		}
	}
	return oldest, found
}

// Pop returns the next frame to play into dst (which must be frameBytes long).
// It returns true if dst was filled with audio (either a real or concealed
// frame), false if there is nothing to play yet (pre-roll not reached, or buffer
// drained — caller should output silence). dst is always fully written when true.
func (jb *jitterBuffer) Pop(dst []byte) bool {
	jb.mu.Lock()
	defer jb.mu.Unlock()

	if !jb.started {
		// Wait until we have enough pre-roll before starting playout.
		if len(jb.frames) < jbTargetFrames {
			return false
		}
		// Begin playout from the oldest buffered frame.
		oldest, ok := jb.oldestSeqLocked()
		if !ok {
			return false
		}
		jb.nextSeq = oldest
		jb.haveNext = true
		jb.started = true
	}

	if !jb.haveNext {
		return false
	}

	if frame, ok := jb.frames[jb.nextSeq]; ok {
		// Real frame available, in order.
		copy(dst, frame)
		delete(jb.frames, jb.nextSeq)
		copy(jb.lastFrame, frame)
		jb.concealCount = 0
		jb.nextSeq++
		return true
	}

	// Expected frame is missing. If the buffer is completely empty we have
	// genuinely run dry: stall playout (output silence) and re-arm pre-roll so a
	// fresh burst rebuilds depth instead of stuttering frame-by-frame.
	if len(jb.frames) == 0 {
		jb.started = false
		jb.haveNext = false
		jb.concealCount = 0
		return false
	}

	// We have later frames buffered but the next one is lost: conceal it (PLC) by
	// repeating the last decoded frame at decaying amplitude, then advance so the
	// later frames eventually play. Cap concealment so a long gap fades to silence
	// rather than droning.
	if jb.concealCount >= jbMaxFrames {
		// Long loss: drop the hole, jump to the oldest buffered frame.
		if oldest, ok := jb.oldestSeqLocked(); ok {
			jb.nextSeq = oldest
			jb.concealCount = 0
			// Play that frame on the next Pop; for now emit silence.
		}
		for i := range dst {
			dst[i] = 0
		}
		return true
	}

	jb.concealFrame(dst)
	jb.concealCount++
	jb.nextSeq++
	return true
}

// concealFrame writes an attenuated copy of lastFrame into dst. Gain decays with
// the number of consecutive concealed frames (0.5x, 0.25x, ...), fading the
// repeated audio toward silence over a run of losses. Caller must hold mu.
func (jb *jitterBuffer) concealFrame(dst []byte) {
	gain := math.Pow(0.5, float64(jb.concealCount+1)) // first conceal = 0.5x
	samples := frameBytes / 2
	for i := 0; i < samples; i++ {
		v := int16(binary.LittleEndian.Uint16(jb.lastFrame[i*2 : i*2+2]))
		scaled := int32(float64(v) * gain)
		binary.LittleEndian.PutUint16(dst[i*2:i*2+2], uint16(int16(scaled)))
	}
}

// AudioEngine manages hardware microphone/speaker streams and network RTP streams.
type AudioEngine struct {
	app            *App
	ctx            *malgo.AllocatedContext
	captureDevice  *malgo.Device
	playbackDevice *malgo.Device

	selectedMicID     string
	selectedSpeakerID string

	isMuted        int32 // atomic bool (0 or 1)
	isCallActive   int32 // atomic bool (0 or 1)
	micAccumulator []byte

	// jitter is the receive/playback buffer (replaces the naive FIFO).
	jitter *jitterBuffer
	// playbackScratch is a pre-allocated frame-sized buffer used only by the
	// realtime playback callback so it never allocates on the audio thread.
	playbackScratch []byte

	// Latest UI meter levels, stored as math.Float64bits so the realtime audio
	// callbacks can publish without allocating or emitting events. A separate
	// ticker goroutine reads these and emits the Wails events off-thread.
	micLevelBits     uint64 // atomic
	speakerLevelBits uint64 // atomic

	// RTP Networking
	rtpConn       *net.UDPConn
	localRTPPort  int
	remoteRTPAddr *net.UDPAddr
	codec         string // "PCMU" or "PCMA"

	// RTP Headers
	seqNumber uint32 // atomic incremented
	timestamp uint32 // atomic incremented
	ssrc      uint32
	// markNext is set so the next outbound RTP packet carries the marker bit
	// (start of a talkspurt: first packet of the call and after unmute).
	markNext int32 // atomic bool

	// SRTP (SDES) contexts; non-nil only when media encryption is negotiated.
	// srtpRecv is used only by the receive goroutine. srtpSend is shared between
	// the capture goroutine and DTMF sends, so outbound packets are serialized by
	// rtpSendMu (held only briefly around encrypt+write).
	srtpEnabled bool
	srtpSend    *srtp.Context
	srtpRecv    *srtp.Context
	rtpSendMu   sync.Mutex

	stopChan chan struct{}
	wg       sync.WaitGroup
}

func NewAudioEngine(app *App) (*AudioEngine, error) {
	// Initialize malgo context
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to init malgo context: %v", err)
	}

	// Generate random SSRC
	var ssrcBytes [4]byte
	_, _ = rand.Read(ssrcBytes[:])
	ssrc := binary.BigEndian.Uint32(ssrcBytes[:])

	ae := &AudioEngine{
		app:               app,
		ctx:               ctx,
		selectedMicID:     "default",
		selectedSpeakerID: "default",
		ssrc:              ssrc,
		jitter:            newJitterBuffer(),
		playbackScratch:   make([]byte, frameBytes),
	}

	return ae, nil
}

// Close frees the audio context.
func (ae *AudioEngine) Close() {
	ae.StopCall()
	if ae.ctx != nil {
		ae.ctx.Uninit()
		ae.ctx.Free()
		ae.ctx = nil
	}
}

// GetDevices returns lists of available microphones (capture) and speakers (playback).
func (ae *AudioEngine) GetDevices() ([]AudioDevice, []AudioDevice, error) {
	if ae.ctx == nil {
		return nil, nil, fmt.Errorf("audio context uninitialized")
	}

	captureInfos, err := ae.ctx.Devices(malgo.Capture)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list capture devices: %v", err)
	}

	playbackInfos, err := ae.ctx.Devices(malgo.Playback)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list playback devices: %v", err)
	}

	mics := []AudioDevice{{ID: "default", Name: "Default Microphone"}}
	for _, info := range captureInfos {
		mics = append(mics, AudioDevice{
			ID:   info.ID.String(),
			Name: info.Name(),
		})
	}

	speakers := []AudioDevice{{ID: "default", Name: "Default Speaker"}}
	for _, info := range playbackInfos {
		speakers = append(speakers, AudioDevice{
			ID:   info.ID.String(),
			Name: info.Name(),
		})
	}

	return mics, speakers, nil
}

// SetDevices sets the preferred device IDs.
func (ae *AudioEngine) SetDevices(micID, speakerID string) {
	ae.selectedMicID = micID
	ae.selectedSpeakerID = speakerID
}

// MuteMicrophone toggles input muting.
func (ae *AudioEngine) MuteMicrophone(mute bool) {
	if mute {
		atomic.StoreInt32(&ae.isMuted, 1)
	} else {
		atomic.StoreInt32(&ae.isMuted, 0)
		// Unmuting starts a new talkspurt: mark the next outbound packet.
		atomic.StoreInt32(&ae.markNext, 1)
	}
}

// AllocateRTPPort allocates a random UDP port and sets up the socket.
func (ae *AudioEngine) AllocateRTPPort() (int, error) {
	// If already open, close it
	if ae.rtpConn != nil {
		ae.rtpConn.Close()
	}

	addr, err := net.ResolveUDPAddr("udp", "0.0.0.0:0")
	if err != nil {
		return 0, err
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return 0, err
	}

	ae.rtpConn = conn
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	ae.localRTPPort = localAddr.Port

	return ae.localRTPPort, nil
}

// StartCall starts capturing, playing and transmitting audio. When both SRTP key
// material arguments are supplied, media is encrypted with SRTP (SDES); otherwise
// plaintext RTP is used. localSRTP keys our outbound stream; remoteSRTP decrypts
// the inbound stream.
func (ae *AudioEngine) StartCall(remoteIP string, remotePort int, codec string, localSRTP, remoteSRTP *SRTPKeyMaterial) error {
	if atomic.LoadInt32(&ae.isCallActive) == 1 {
		return fmt.Errorf("call already active")
	}

	remoteAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", remoteIP, remotePort))
	if err != nil {
		return fmt.Errorf("invalid remote RTP address: %v", err)
	}
	ae.remoteRTPAddr = remoteAddr
	ae.codec = codec

	// Configure SRTP contexts if encryption was negotiated for this call.
	ae.srtpEnabled = false
	ae.srtpSend = nil
	ae.srtpRecv = nil
	if localSRTP != nil && remoteSRTP != nil {
		profile := srtp.ProtectionProfileAes128CmHmacSha1_80
		sendCtx, err := srtp.CreateContext(localSRTP.Key, localSRTP.Salt, profile)
		if err != nil {
			return fmt.Errorf("failed to create SRTP send context: %v", err)
		}
		recvCtx, err := srtp.CreateContext(remoteSRTP.Key, remoteSRTP.Salt, profile)
		if err != nil {
			return fmt.Errorf("failed to create SRTP receive context: %v", err)
		}
		ae.srtpSend = sendCtx
		ae.srtpRecv = recvCtx
		ae.srtpEnabled = true
	}

	// Reset counters and buffers.
	atomic.StoreUint32(&ae.seqNumber, 0)
	atomic.StoreUint32(&ae.timestamp, 0)
	atomic.StoreInt32(&ae.markNext, 1) // first packet of the call gets the marker bit
	atomic.StoreUint64(&ae.micLevelBits, 0)
	atomic.StoreUint64(&ae.speakerLevelBits, 0)
	ae.jitter.Reset()
	ae.micAccumulator = nil
	atomic.StoreInt32(&ae.isCallActive, 1)

	ae.stopChan = make(chan struct{})

	// Start network receive loop.
	ae.wg.Add(1)
	go ae.rtpReceiveLoop()

	// Start the UI level emitter so the realtime audio callbacks never call
	// EmitEvent or allocate on the audio thread.
	ae.wg.Add(1)
	go ae.levelEmitLoop()

	// Initialize Audio Devices
	err = ae.startHardwareDevices()
	if err != nil {
		ae.StopCall()
		return fmt.Errorf("failed to start hardware audio: %v", err)
	}

	return nil
}

// StopCall terminates audio processing and releases resources.
func (ae *AudioEngine) StopCall() {
	if atomic.LoadInt32(&ae.isCallActive) == 0 {
		return
	}

	atomic.StoreInt32(&ae.isCallActive, 0)
	if ae.stopChan != nil {
		close(ae.stopChan)
	}

	ae.stopHardwareDevices()

	// Close the socket to unblock rtpReceiveLoop's ReadFrom, but do NOT nil the
	// field until the goroutines have stopped — the receive loop holds its own
	// local reference, and sendRTPPacket nil-guards, so closing is enough here.
	if ae.rtpConn != nil {
		_ = ae.rtpConn.Close()
	}

	ae.wg.Wait()

	// Safe now: no goroutine touches these fields after wg.Wait().
	ae.rtpConn = nil
	ae.srtpEnabled = false
	ae.srtpSend = nil
	ae.srtpRecv = nil
}

func (ae *AudioEngine) findDeviceID(deviceType malgo.DeviceType, idStr string) *malgo.DeviceID {
	if idStr == "default" || idStr == "" {
		return nil
	}

	infos, err := ae.ctx.Devices(deviceType)
	if err != nil {
		return nil
	}

	for _, info := range infos {
		if info.ID.String() == idStr {
			devID := info.ID
			return &devID
		}
	}
	return nil
}

func (ae *AudioEngine) startHardwareDevices() error {
	// Configure Capture (Microphone) at 8000Hz mono
	captureID := ae.findDeviceID(malgo.Capture, ae.selectedMicID)
	captureConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	captureConfig.Capture.Format = malgo.FormatS16
	captureConfig.Capture.Channels = 1
	captureConfig.SampleRate = audioSampleRate
	if captureID != nil {
		captureConfig.Capture.DeviceID = captureID.Pointer()
	}

	captureCallbacks := malgo.DeviceCallbacks{
		Data: ae.captureCallback,
	}

	var err error
	ae.captureDevice, err = malgo.InitDevice(ae.ctx.Context, captureConfig, captureCallbacks)
	if err != nil {
		return fmt.Errorf("failed to init mic device: %v", err)
	}
	ae.warnIfSampleRateMismatch("microphone", ae.captureDevice)

	// Configure Playback (Speaker) at 8000Hz mono
	playbackID := ae.findDeviceID(malgo.Playback, ae.selectedSpeakerID)
	playbackConfig := malgo.DefaultDeviceConfig(malgo.Playback)
	playbackConfig.Playback.Format = malgo.FormatS16
	playbackConfig.Playback.Channels = 1
	playbackConfig.SampleRate = audioSampleRate
	if playbackID != nil {
		playbackConfig.Playback.DeviceID = playbackID.Pointer()
	}

	playbackCallbacks := malgo.DeviceCallbacks{
		Data: ae.playbackCallback,
	}

	ae.playbackDevice, err = malgo.InitDevice(ae.ctx.Context, playbackConfig, playbackCallbacks)
	if err != nil {
		if ae.captureDevice != nil {
			ae.captureDevice.Uninit()
			ae.captureDevice = nil
		}
		return fmt.Errorf("failed to init speaker device: %v", err)
	}
	ae.warnIfSampleRateMismatch("speaker", ae.playbackDevice)

	// Start Devices
	err = ae.captureDevice.Start()
	if err != nil {
		ae.stopHardwareDevices()
		return fmt.Errorf("failed to start mic: %v", err)
	}

	err = ae.playbackDevice.Start()
	if err != nil {
		ae.stopHardwareDevices()
		return fmt.Errorf("failed to start speaker: %v", err)
	}

	return nil
}

// warnIfSampleRateMismatch reads back the device's actual sample rate after init
// and surfaces a warning if it isn't 8kHz. The G.711 packetization math assumes
// 8kHz S16 mono, so a mismatch means pitch/timing distortion.
func (ae *AudioEngine) warnIfSampleRateMismatch(label string, dev *malgo.Device) {
	if dev == nil {
		return
	}
	actual := dev.SampleRate()
	if actual != audioSampleRate {
		msg := fmt.Sprintf("WARNING: %s opened at %d Hz, expected %d Hz; audio may be distorted (G.711 assumes 8kHz S16 mono)", label, actual, audioSampleRate)
		if ae.app != nil {
			ae.app.EmitEvent("sip:log", msg)
		} else {
			println(msg)
		}
	}
}

func (ae *AudioEngine) stopHardwareDevices() {
	if ae.captureDevice != nil {
		_ = ae.captureDevice.Stop()
		ae.captureDevice.Uninit()
		ae.captureDevice = nil
	}
	if ae.playbackDevice != nil {
		_ = ae.playbackDevice.Stop()
		ae.playbackDevice.Uninit()
		ae.playbackDevice = nil
	}
}

// captureCallback receives mic PCM samples. This runs on miniaudio's realtime
// audio thread: it must not block, allocate per call, or emit Wails events. RMS
// is published into an atomic field; the levelEmitLoop goroutine emits the event.
func (ae *AudioEngine) captureCallback(pOutputSample, pInputSamples []byte, frameCount uint32) {
	if atomic.LoadInt32(&ae.isCallActive) == 0 {
		return
	}

	// Compute mic level (even when muted, so the user sees they're talking) and
	// publish it lock-free for the emitter goroutine.
	rms := calculateRMS(pInputSamples)
	atomic.StoreUint64(&ae.micLevelBits, math.Float64bits(rms))

	if atomic.LoadInt32(&ae.isMuted) == 1 {
		return
	}

	ae.micAccumulator = append(ae.micAccumulator, pInputSamples...)

	// VoIP packetization size is 20ms (320 bytes = 160 samples of 16-bit PCM @ 8kHz)
	for len(ae.micAccumulator) >= frameBytes {
		chunk := ae.micAccumulator[:frameBytes]
		ae.micAccumulator = ae.micAccumulator[frameBytes:]

		// Encode to G.711 (resulting in 160 bytes)
		var encoded []byte
		if ae.codec == "PCMA" {
			encoded = g711.EncodeAlaw(chunk)
		} else {
			encoded = g711.EncodeUlaw(chunk)
		}

		ae.sendRTPPacket(encoded)
	}
}

// playbackCallback writes PCM samples to speakers. Runs on the realtime audio
// thread: it pulls whole frames from the jitter buffer into a pre-allocated
// scratch buffer (no per-call allocation, brief lock only), and publishes the
// speaker RMS into an atomic field rather than emitting an event here.
func (ae *AudioEngine) playbackCallback(pOutputSample, pInputSamples []byte, frameCount uint32) {
	reqBytes := int(frameCount) * 2 // S16 = 2 bytes per sample

	written := 0
	for written < reqBytes {
		remaining := reqBytes - written
		if remaining >= frameBytes {
			// Pull a whole frame directly into the output buffer.
			dst := pOutputSample[written : written+frameBytes]
			if ae.jitter.Pop(dst) {
				written += frameBytes
				continue
			}
			// Nothing to play: fill the rest with silence and stop.
			break
		}

		// Partial trailing chunk (output buffer not a frame multiple): use the
		// pre-allocated scratch frame and copy only what fits.
		if ae.jitter.Pop(ae.playbackScratch) {
			copy(pOutputSample[written:reqBytes], ae.playbackScratch[:remaining])
			written = reqBytes
		}
		break
	}

	// Zero-fill any remainder (underrun / drained buffer).
	for i := written; i < reqBytes; i++ {
		pOutputSample[i] = 0
	}

	// Publish speaker level lock-free for the emitter goroutine.
	rms := calculateRMS(pOutputSample[:reqBytes])
	atomic.StoreUint64(&ae.speakerLevelBits, math.Float64bits(rms))
}

// levelEmitLoop emits the mic/speaker level UI events at a fixed rate from the
// atomically-published values, keeping EmitEvent off the realtime audio thread.
func (ae *AudioEngine) levelEmitLoop() {
	defer ae.wg.Done()

	// Capture stopChan locally: a subsequent StartCall (after StopCall's wg.Wait)
	// may reassign the field, but this loop owns its own reference.
	stop := ae.stopChan

	ticker := time.NewTicker(time.Second / levelEmitHz)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			// Emit a final zero so the UI meters settle when the call ends.
			ae.app.EmitEvent("audio:mic_level", 0.0)
			ae.app.EmitEvent("audio:speaker_level", 0.0)
			return
		case <-ticker.C:
			mic := math.Float64frombits(atomic.LoadUint64(&ae.micLevelBits))
			spk := math.Float64frombits(atomic.LoadUint64(&ae.speakerLevelBits))
			ae.app.EmitEvent("audio:mic_level", mic)
			ae.app.EmitEvent("audio:speaker_level", spk)
		}
	}
}

func (ae *AudioEngine) sendRTPPacket(payload []byte) {
	if ae.rtpConn == nil || ae.remoteRTPAddr == nil {
		return
	}

	// Increment headers
	seq := atomic.AddUint32(&ae.seqNumber, 1)
	ts := atomic.AddUint32(&ae.timestamp, uint32(len(payload))) // payload len matches samples because G.711 has 1 byte per sample

	payloadType := uint8(0) // PCMU
	if ae.codec == "PCMA" {
		payloadType = uint8(8) // PCMA
	}

	// Set the marker bit on the first packet of a talkspurt (call start / unmute).
	marker := atomic.CompareAndSwapInt32(&ae.markNext, 1, 0)

	packet := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			Padding:        false,
			Extension:      false,
			Marker:         marker,
			PayloadType:    payloadType,
			SequenceNumber: uint16(seq & 0xFFFF),
			Timestamp:      ts,
			SSRC:           ae.ssrc,
		},
		Payload: payload,
	}

	raw, err := packet.Marshal()
	if err != nil {
		return
	}

	// Serialize encrypt+write so audio and DTMF sends never race the SRTP context
	// or interleave on the socket. The lock is held only briefly (~once per 20ms).
	ae.rtpSendMu.Lock()
	defer ae.rtpSendMu.Unlock()

	// Encrypt the marshaled RTP packet in place when SRTP is active.
	if ae.srtpEnabled && ae.srtpSend != nil {
		enc, err := ae.srtpSend.EncryptRTP(nil, raw, nil)
		if err != nil {
			return
		}
		raw = enc
	}

	_, _ = ae.rtpConn.WriteTo(raw, ae.remoteRTPAddr)
}

// dtmfPayloadType is the RTP payload type we negotiate for telephone-event
// (RFC 4733). 101 is the universal de-facto default offered by SIP endpoints.
const dtmfPayloadType uint8 = 101

// dtmfEvent maps a DTMF character to its RFC 4733 event code.
func dtmfEvent(digit string) (uint8, bool) {
	if len(digit) != 1 {
		return 0, false
	}
	c := digit[0]
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c == '*':
		return 10, true
	case c == '#':
		return 11, true
	case c >= 'A' && c <= 'D':
		return 12 + (c - 'A'), true
	case c >= 'a' && c <= 'd':
		return 12 + (c - 'a'), true
	}
	return 0, false
}

// SendDTMF transmits a DTMF digit as an in-band RFC 4733 telephone-event over the
// active RTP stream. It returns quickly; the timed event packets are sent from a
// background goroutine.
func (ae *AudioEngine) SendDTMF(digit string) error {
	if atomic.LoadInt32(&ae.isCallActive) == 0 {
		return fmt.Errorf("no active call")
	}
	ev, ok := dtmfEvent(digit)
	if !ok {
		return fmt.Errorf("invalid DTMF digit %q", digit)
	}
	ae.wg.Add(1)
	go func() {
		defer ae.wg.Done()
		ae.sendDTMFEvent(ev)
	}()
	return nil
}

// sendDTMFEvent emits the RFC 4733 packet sequence for one event: an initial
// packet (marker set) followed by periodic updates with growing duration, then
// three end packets with the End bit set (per RFC 4733 §2.5.1.4 redundancy).
func (ae *AudioEngine) sendDTMFEvent(event uint8) {
	const (
		volume     = 10  // dBm0 attenuation
		stepTS     = 160 // 20ms @ 8kHz
		toneFrames = 8   // ~160ms tone
		endRepeats = 3
	)
	// All packets of one event share the start timestamp; duration grows.
	startTS := atomic.LoadUint32(&ae.timestamp)

	var duration uint16
	for i := 0; i < toneFrames; i++ {
		if atomic.LoadInt32(&ae.isCallActive) == 0 {
			return
		}
		duration += stepTS
		ae.sendDTMFPacket(event, false, volume, duration, startTS, i == 0)
		time.Sleep(20 * time.Millisecond)
	}
	for i := 0; i < endRepeats; i++ {
		if atomic.LoadInt32(&ae.isCallActive) == 0 {
			return
		}
		ae.sendDTMFPacket(event, true, volume, duration, startTS, false)
	}
}

// sendDTMFPacket builds and sends a single telephone-event RTP packet.
func (ae *AudioEngine) sendDTMFPacket(event uint8, end bool, volume uint8, duration uint16, ts uint32, marker bool) {
	if ae.rtpConn == nil || ae.remoteRTPAddr == nil {
		return
	}

	// RFC 4733 payload: event(8) | E R volume(8) | duration(16)
	payload := make([]byte, 4)
	payload[0] = event
	payload[1] = volume & 0x3F
	if end {
		payload[1] |= 0x80
	}
	binary.BigEndian.PutUint16(payload[2:], duration)

	seq := atomic.AddUint32(&ae.seqNumber, 1)
	packet := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			Marker:         marker,
			PayloadType:    dtmfPayloadType,
			SequenceNumber: uint16(seq & 0xFFFF),
			Timestamp:      ts,
			SSRC:           ae.ssrc,
		},
		Payload: payload,
	}
	raw, err := packet.Marshal()
	if err != nil {
		return
	}

	ae.rtpSendMu.Lock()
	defer ae.rtpSendMu.Unlock()
	if ae.srtpEnabled && ae.srtpSend != nil {
		enc, err := ae.srtpSend.EncryptRTP(nil, raw, nil)
		if err != nil {
			return
		}
		raw = enc
	}
	_, _ = ae.rtpConn.WriteTo(raw, ae.remoteRTPAddr)
}

func (ae *AudioEngine) rtpReceiveLoop() {
	defer ae.wg.Done()

	// Capture the connection locally: StopCall may nil the shared field, but this
	// loop owns its own reference for the lifetime of the call.
	conn := ae.rtpConn
	if conn == nil {
		return
	}

	buf := make([]byte, 2048)
	for {
		if atomic.LoadInt32(&ae.isCallActive) == 0 {
			return
		}

		// Set read deadline to avoid hanging indefinitely on shutdown
		_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, _, err := conn.ReadFrom(buf)

		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			// Connection closed or other error
			return
		}

		raw := buf[:n]
		// Decrypt the SRTP packet first when media encryption is active.
		if ae.srtpEnabled && ae.srtpRecv != nil {
			dec, err := ae.srtpRecv.DecryptRTP(nil, raw, nil)
			if err != nil {
				continue
			}
			raw = dec
		}

		packet := &rtp.Packet{}
		if err := packet.Unmarshal(raw); err != nil {
			continue
		}

		// Decode RTP payload based on negotiated codec
		var pcm []byte
		if packet.PayloadType == 8 { // PCMA
			pcm = g711.DecodeAlaw(packet.Payload)
		} else if packet.PayloadType == 0 { // PCMU
			pcm = g711.DecodeUlaw(packet.Payload)
		} else {
			// Unsupported payload type (could be DTMF telephone-event or other
			// codec): ignore without crashing.
			continue
		}

		// Buffer by sequence number so the jitter buffer can reorder/conceal.
		// Skip oddly-sized payloads that don't form a clean 20ms frame.
		if len(pcm) == 0 {
			continue
		}
		ae.jitter.Insert(packet.SequenceNumber, pcm)
	}
}

// calculateRMS calculates root-mean-square percentage (0.0 to 100.0) of PCM S16 bytes.
func calculateRMS(pcm []byte) float64 {
	if len(pcm) == 0 {
		return 0
	}

	var sum float64
	samples := len(pcm) / 2
	if samples == 0 {
		return 0
	}
	for i := 0; i < samples; i++ {
		val := int16(binary.LittleEndian.Uint16(pcm[i*2 : i*2+2]))
		valF := float64(val)
		sum += valF * valF
	}

	mean := sum / float64(samples)
	rms := math.Sqrt(mean)

	// Scale to 0-100 range. Max int16 is 32767.
	percentage := (rms / 32767.0) * 100.0
	if percentage > 100.0 {
		percentage = 100.0
	}

	// Apply log scaling to make it more visually pleasing/reactive at lower volumes
	if percentage > 0 {
		percentage = math.Log10(percentage)*25 + 50 // Map logarithmic range nicely
	}
	if percentage < 0 || math.IsNaN(percentage) {
		percentage = 0
	}
	if percentage > 100 {
		percentage = 100
	}

	return percentage
}
