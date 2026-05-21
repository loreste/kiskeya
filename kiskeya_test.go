package main

import (
	"bytes"
	"math"
	"testing"

	"github.com/zaf/g711"
)

func encodePCMU(sample int16) byte {
	buf := []byte{byte(sample), byte(sample >> 8)}
	encoded := g711.EncodeUlaw(buf)
	if len(encoded) > 0 {
		return encoded[0]
	}
	return 0
}

func decodePCMU(code byte) int16 {
	decoded := g711.DecodeUlaw([]byte{code})
	if len(decoded) >= 2 {
		return int16(decoded[0]) | (int16(decoded[1]) << 8)
	}
	return 0
}

func encodePCMA(sample int16) byte {
	buf := []byte{byte(sample), byte(sample >> 8)}
	encoded := g711.EncodeAlaw(buf)
	if len(encoded) > 0 {
		return encoded[0]
	}
	return 0
}

func decodePCMA(code byte) int16 {
	decoded := g711.DecodeAlaw([]byte{code})
	if len(decoded) >= 2 {
		return int16(decoded[0]) | (int16(decoded[1]) << 8)
	}
	return 0
}

// TestG711Codecs verifies that G.711 PCMU and PCMA encoding and decoding
// function correctly and preserve the signal properties within standard bounds.
func TestG711Codecs(t *testing.T) {
	// Generate a simple PCM 16-bit sine wave test signal
	testSamples := make([]int16, 100)
	for i := range testSamples {
		testSamples[i] = int16(32767 * math.Sin(2.0*math.Pi*float64(i)/10.0))
	}

	t.Run("PCMU", func(t *testing.T) {
		encoded := make([]byte, len(testSamples))
		for i, sample := range testSamples {
			encoded[i] = encodePCMU(sample)
		}

		decoded := make([]int16, len(testSamples))
		for i, code := range encoded {
			decoded[i] = decodePCMU(code)
		}

		// Verify average quantization error is reasonably low for PCMU
		var totalError float64
		for i := range testSamples {
			diff := math.Abs(float64(testSamples[i] - decoded[i]))
			totalError += diff
		}
		avgError := totalError / float64(len(testSamples))
		if avgError > 500 { // G.711 compression results in some loss, but should be minimal
			t.Errorf("PCMU average quantization error too high: %f", avgError)
		}
	})

	t.Run("PCMA", func(t *testing.T) {
		encoded := make([]byte, len(testSamples))
		for i, sample := range testSamples {
			encoded[i] = encodePCMA(sample)
		}

		decoded := make([]int16, len(testSamples))
		for i, code := range encoded {
			decoded[i] = decodePCMA(code)
		}

		// Verify average quantization error is reasonably low for PCMA
		var totalError float64
		for i := range testSamples {
			diff := math.Abs(float64(testSamples[i] - decoded[i]))
			totalError += diff
		}
		avgError := totalError / float64(len(testSamples))
		if avgError > 500 {
			t.Errorf("PCMA average quantization error too high: %f", avgError)
		}
	})
}

// TestSDPAndCodecNegotiation checks that SDP offers/answers are constructed
// and parsed correctly.
func TestSDPAndCodecNegotiation(t *testing.T) {
	se := &SIPEngine{
		localIP: "192.168.1.50",
	}

	t.Run("Create and Parse SDP Offer", func(t *testing.T) {
		offer := se.createSDPOffer(40002, nil)
		if len(offer) == 0 {
			t.Fatal("generated empty SDP offer")
		}

		if !bytes.Contains(offer, []byte("c=IN IP4 192.168.1.50")) {
			t.Errorf("SDP offer lacks correct connection address: %s", string(offer))
		}

		if !bytes.Contains(offer, []byte("m=audio 40002 RTP/AVP 0 8 101")) {
			t.Errorf("SDP offer lacks correct media announcement: %s", string(offer))
		}
		if !bytes.Contains(offer, []byte("telephone-event/8000")) {
			t.Errorf("SDP offer should advertise telephone-event (DTMF): %s", string(offer))
		}

		// Parse back the SDP offer
		ip, port, codec, srtp, err := se.parseSDP(offer)
		if err != nil {
			t.Fatalf("failed to parse generated SDP offer: %v", err)
		}

		if ip != "192.168.1.50" {
			t.Errorf("expected IP 192.168.1.50, got %s", ip)
		}
		if port != 40002 {
			t.Errorf("expected port 40002, got %d", port)
		}
		if codec != "PCMU" { // PCMU is first/preferred format "0"
			t.Errorf("expected codec PCMU, got %s", codec)
		}
		if srtp != nil {
			t.Errorf("plain offer should not carry SRTP keys")
		}
	})

	t.Run("Create and Parse SDP Answer", func(t *testing.T) {
		answer := se.createSDPAnswer(40004, "PCMA", nil, false)
		if len(answer) == 0 {
			t.Fatal("generated empty SDP answer")
		}

		if !bytes.Contains(answer, []byte("m=audio 40004 RTP/AVP 8")) {
			t.Errorf("SDP answer lacks correct media announcement for PCMA: %s", string(answer))
		}

		// Parse back the SDP answer
		ip, port, codec, srtp, err := se.parseSDP(answer)
		if err != nil {
			t.Fatalf("failed to parse generated SDP answer: %v", err)
		}

		if ip != "192.168.1.50" {
			t.Errorf("expected IP 192.168.1.50, got %s", ip)
		}
		if port != 40004 {
			t.Errorf("expected port 40004, got %d", port)
		}
		if codec != "PCMA" {
			t.Errorf("expected codec PCMA, got %s", codec)
		}
		if srtp != nil {
			t.Errorf("plain answer should not carry SRTP keys")
		}
	})

	t.Run("SRTP SAVP Offer carries crypto and round-trips", func(t *testing.T) {
		key, err := GenerateSRTPKey()
		if err != nil {
			t.Fatalf("failed to generate SRTP key: %v", err)
		}

		offer := se.createSDPOffer(40006, key)
		if !bytes.Contains(offer, []byte("m=audio 40006 RTP/SAVP 0 8 101")) {
			t.Errorf("secure offer should use RTP/SAVP profile: %s", string(offer))
		}
		if !bytes.Contains(offer, []byte("a=crypto:1 AES_CM_128_HMAC_SHA1_80 inline:")) {
			t.Errorf("secure offer should carry an a=crypto line: %s", string(offer))
		}

		_, _, _, parsed, err := se.parseSDP(offer)
		if err != nil {
			t.Fatalf("failed to parse secure offer: %v", err)
		}
		if parsed == nil {
			t.Fatal("expected SRTP key material parsed from secure offer")
		}
		if !bytes.Equal(parsed.Key, key.Key) || !bytes.Equal(parsed.Salt, key.Salt) {
			t.Errorf("parsed SRTP key material does not match generated key")
		}
	})
}

// TestDTMFEvent verifies the RFC 4733 digit-to-event mapping.
func TestDTMFEvent(t *testing.T) {
	cases := map[string]uint8{
		"0": 0, "9": 9, "*": 10, "#": 11, "A": 12, "D": 15, "d": 15,
	}
	for digit, want := range cases {
		got, ok := dtmfEvent(digit)
		if !ok || got != want {
			t.Errorf("dtmfEvent(%q) = %d,%v; want %d,true", digit, got, ok, want)
		}
	}
	for _, bad := range []string{"", "E", "12", "!"} {
		if _, ok := dtmfEvent(bad); ok {
			t.Errorf("dtmfEvent(%q) should be invalid", bad)
		}
	}
}

// TestSRTPKeyMaterial verifies SDES crypto attribute generation and parsing.
func TestSRTPKeyMaterial(t *testing.T) {
	key, err := GenerateSRTPKey()
	if err != nil {
		t.Fatalf("GenerateSRTPKey failed: %v", err)
	}
	if len(key.Key) != srtpMasterKeyLen || len(key.Salt) != srtpMasterSaltLen {
		t.Fatalf("unexpected key/salt length: %d/%d", len(key.Key), len(key.Salt))
	}

	attr := key.CryptoAttribute(1)
	parsed, err := ParseCryptoAttribute(attr)
	if err != nil {
		t.Fatalf("ParseCryptoAttribute failed for %q: %v", attr, err)
	}
	if !bytes.Equal(parsed.Key, key.Key) || !bytes.Equal(parsed.Salt, key.Salt) {
		t.Errorf("round-tripped key material mismatch")
	}

	// Tolerate optional lifetime/MKI suffixes on the inline value.
	withSuffix := attr + "|2^20|1:4"
	if _, err := ParseCryptoAttribute(withSuffix); err != nil {
		t.Errorf("ParseCryptoAttribute should tolerate lifetime/MKI suffix: %v", err)
	}

	// Reject an unsupported crypto suite.
	if _, err := ParseCryptoAttribute("1 AES_256_CM_HMAC_SHA1_80 inline:abcd"); err == nil {
		t.Errorf("expected error for unsupported crypto suite")
	}
}

// TestSTUNResponseParsing validates that the STUN response bytes are decoded correctly.
func TestSTUNResponseParsing(t *testing.T) {
	// Construct a mock STUN response:
	// Message Type: 0x0101 (Binding Success Response)
	// Message Length: 0x000c (12 bytes of attributes)
	// Magic Cookie: 0x2112A442
	// Transaction ID: 12 bytes of zeros
	// Attribute 1: XOR-MAPPED-ADDRESS
	//   Type: 0x0020
	//   Length: 8 bytes
	//   Value: 0x00 (pad), 0x01 (IPv4 family), 0x112B (XOR'd port), 0xE1BAB543 (XOR'd IP)
	mockResponse := []byte{
		0x01, 0x01, // Binding Success
		0x00, 0x0c, // Attribute length: 12 bytes
		0x21, 0x12, 0xa4, 0x42, // Magic cookie
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // Transaction ID

		// XOR-MAPPED-ADDRESS attribute
		0x00, 0x20, // Attribute type: 0x0020
		0x00, 0x08, // Attribute length: 8
		0x00,       // Pad
		0x01,       // IPv4 Family
		0x11, 0x2b, // XOR'd port for 12345 (0x3039)
		0xe1, 0xba, 0xa5, 0x43, // XOR'd IP for 192.168.1.1 (0xC0A80101)
	}

	ip, err := parseSTUNResponse(mockResponse)
	if err != nil {
		t.Fatalf("failed to parse mock STUN response: %v", err)
	}

	expectedIP := "192.168.1.1"
	if ip != expectedIP {
		t.Errorf("expected IP %s, got %s", expectedIP, ip)
	}
}
