package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
)

// SDES-SRTP (RFC 4568) support for the single crypto suite we negotiate:
// AES_CM_128_HMAC_SHA1_80, which uses a 16-byte master key and 14-byte master
// salt (30 bytes of inline key material). This is the interoperable default used
// by most SIP softphones and PBXs (Asterisk, FreeSWITCH, Linphone).

const (
	srtpCryptoSuite   = "AES_CM_128_HMAC_SHA1_80"
	srtpMasterKeyLen  = 16
	srtpMasterSaltLen = 14
)

// SRTPKeyMaterial holds the master key and salt for a single direction.
type SRTPKeyMaterial struct {
	Key  []byte // 16 bytes
	Salt []byte // 14 bytes
}

// inline returns the base64 of (key || salt) used in an a=crypto inline value.
func (k *SRTPKeyMaterial) inline() string {
	buf := make([]byte, 0, srtpMasterKeyLen+srtpMasterSaltLen)
	buf = append(buf, k.Key...)
	buf = append(buf, k.Salt...)
	return base64.StdEncoding.EncodeToString(buf)
}

// CryptoAttribute returns the a=crypto attribute value (without the "crypto:"
// key) for the given tag, e.g. "1 AES_CM_128_HMAC_SHA1_80 inline:<base64>".
func (k *SRTPKeyMaterial) CryptoAttribute(tag int) string {
	return fmt.Sprintf("%d %s inline:%s", tag, srtpCryptoSuite, k.inline())
}

// GenerateSRTPKey returns fresh random master key + salt from a CSPRNG.
func GenerateSRTPKey() (*SRTPKeyMaterial, error) {
	km := &SRTPKeyMaterial{
		Key:  make([]byte, srtpMasterKeyLen),
		Salt: make([]byte, srtpMasterSaltLen),
	}
	if _, err := rand.Read(km.Key); err != nil {
		return nil, err
	}
	if _, err := rand.Read(km.Salt); err != nil {
		return nil, err
	}
	return km, nil
}

// ParseCryptoAttribute parses an a=crypto attribute value and returns the key
// material if it advertises the supported suite. Optional session parameters,
// key lifetime and MKI ("|...") are tolerated and ignored.
func ParseCryptoAttribute(value string) (*SRTPKeyMaterial, error) {
	fields := strings.Fields(value)
	if len(fields) < 3 {
		return nil, fmt.Errorf("malformed crypto attribute")
	}
	if fields[1] != srtpCryptoSuite {
		return nil, fmt.Errorf("unsupported crypto suite %q", fields[1])
	}

	// The key parameter is "inline:<base64>[|lifetime][|MKI:len]". There may be
	// several key parameters; use the first inline one.
	var inline string
	for _, f := range fields[2:] {
		if strings.HasPrefix(f, "inline:") {
			inline = strings.TrimPrefix(f, "inline:")
			break
		}
	}
	if inline == "" {
		return nil, fmt.Errorf("no inline key in crypto attribute")
	}
	if idx := strings.Index(inline, "|"); idx >= 0 {
		inline = inline[:idx]
	}

	raw, err := base64.StdEncoding.DecodeString(inline)
	if err != nil {
		return nil, fmt.Errorf("invalid base64 key material: %v", err)
	}
	if len(raw) < srtpMasterKeyLen+srtpMasterSaltLen {
		return nil, fmt.Errorf("key material too short (%d bytes)", len(raw))
	}

	return &SRTPKeyMaterial{
		Key:  raw[:srtpMasterKeyLen],
		Salt: raw[srtpMasterKeyLen : srtpMasterKeyLen+srtpMasterSaltLen],
	}, nil
}
