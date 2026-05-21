package main

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"time"
)

// GetPublicIP contacts a STUN server over UDP to discover the public-facing
// IP address of the local network interface.
func GetPublicIP(stunServer string) (string, error) {
	if stunServer == "" {
		return "", fmt.Errorf("stun server address is empty")
	}

	// Connect to the STUN server via UDP
	raddr, err := net.ResolveUDPAddr("udp", stunServer)
	if err != nil {
		return "", fmt.Errorf("failed to resolve STUN server address: %v", err)
	}

	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return "", fmt.Errorf("failed to dial STUN server: %v", err)
	}
	defer conn.Close()

	// STUN Binding Request (RFC 5389)
	// Header: 20 bytes
	//   - Type: 2 bytes (0x0001 for Binding Request)
	//   - Length: 2 bytes (0x0000 payload length)
	//   - Magic Cookie: 4 bytes (0x2112A442)
	//   - Transaction ID: 12 bytes
	var req [20]byte
	req[0] = 0x00
	req[1] = 0x01
	binary.BigEndian.PutUint16(req[2:4], 0)
	binary.BigEndian.PutUint32(req[4:8], 0x2112A442)
	
	_, err = rand.Read(req[8:20])
	if err != nil {
		return "", fmt.Errorf("failed to generate STUN transaction ID: %v", err)
	}

	// Send STUN Binding Request
	_, err = conn.Write(req[:])
	if err != nil {
		return "", fmt.Errorf("failed to send STUN request: %v", err)
	}

	// Wait for response (with deadline)
	err = conn.SetReadDeadline(time.Now().Add(2500 * time.Millisecond))
	if err != nil {
		return "", err
	}

	res := make([]byte, 1024)
	n, _, err := conn.ReadFrom(res)
	if err != nil {
		return "", fmt.Errorf("failed to read STUN response: %v", err)
	}

	return parseSTUNResponse(res[:n])
}

// parseSTUNResponse parses the response bytes from a STUN server to find
// the public IP address in the MAPPED-ADDRESS or XOR-MAPPED-ADDRESS attributes.
func parseSTUNResponse(res []byte) (string, error) {
	if len(res) < 20 {
		return "", fmt.Errorf("STUN response too short")
	}

	messageType := binary.BigEndian.Uint16(res[0:2])
	if messageType != 0x0101 { // Binding Success Response
		return "", fmt.Errorf("STUN message type is not success response: %x", messageType)
	}

	length := int(binary.BigEndian.Uint16(res[2:4]))
	if len(res) < 20+length {
		return "", fmt.Errorf("STUN response body incomplete")
	}

	offset := 20
	for offset < 20+length {
		if offset+4 > len(res) {
			break
		}
		attrType := binary.BigEndian.Uint16(res[offset : offset+2])
		attrLen := int(binary.BigEndian.Uint16(res[offset+2 : offset+4]))
		offset += 4

		if offset+attrLen > len(res) {
			break
		}

		attrVal := res[offset : offset+attrLen]
		offset += (attrLen + 3) &^ 3 // Align to 32-bit boundary

		// MAPPED-ADDRESS (0x0001)
		if attrType == 0x0001 {
			if len(attrVal) < 8 {
				continue
			}
			family := attrVal[1]
			if family == 1 { // IPv4
				ip := net.IP(attrVal[4:8]).String()
				return ip, nil
			}
		}

		// XOR-MAPPED-ADDRESS (0x0020)
		if attrType == 0x0020 {
			if len(attrVal) < 8 {
				continue
			}
			family := attrVal[1]
			if family == 1 { // IPv4
				xip := binary.BigEndian.Uint32(attrVal[4:8])
				ipVal := xip ^ 0x2112A442
				
				ipBytes := make([]byte, 4)
				binary.BigEndian.PutUint32(ipBytes, ipVal)
				ip := net.IP(ipBytes).String()
				return ip, nil
			}
		}
	}

	return "", fmt.Errorf("no public IP address attribute found in STUN response")
}
