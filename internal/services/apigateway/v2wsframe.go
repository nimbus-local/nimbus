package apigateway

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

const (
	wsOpContinuation byte = 0x0
	wsOpText         byte = 0x1
	wsOpBinary       byte = 0x2
	wsOpClose        byte = 0x8
	wsOpPing         byte = 0x9
	wsOpPong         byte = 0xA
)

type wsFrame struct {
	fin     bool
	opcode  byte
	payload []byte
}

func wsAcceptKey(clientKey string) string {
	h := sha1.New()
	h.Write([]byte(strings.TrimSpace(clientKey) + wsGUID))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// wsUpgrade performs the server-side WebSocket handshake via HTTP hijacking.
func wsUpgrade(w http.ResponseWriter, r *http.Request) (net.Conn, *bufio.Reader, error) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return nil, nil, fmt.Errorf("not a websocket upgrade request")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return nil, nil, fmt.Errorf("missing Sec-WebSocket-Key")
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("server does not support hijacking")
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		return nil, nil, err
	}

	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + wsAcceptKey(key) + "\r\n" +
		"\r\n"
	if _, err := conn.Write([]byte(resp)); err != nil {
		conn.Close()
		return nil, nil, err
	}
	return conn, brw.Reader, nil
}

// wsRead reads one complete WebSocket frame. Client frames are always masked per RFC 6455 §5.3.
func wsRead(br *bufio.Reader) (*wsFrame, error) {
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(br, hdr); err != nil {
		return nil, err
	}
	fin := hdr[0]&0x80 != 0
	opcode := hdr[0] & 0x0F
	masked := hdr[1]&0x80 != 0
	payLen := int64(hdr[1] & 0x7F)

	switch payLen {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(br, ext[:]); err != nil {
			return nil, err
		}
		payLen = int64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(br, ext[:]); err != nil {
			return nil, err
		}
		payLen = int64(binary.BigEndian.Uint64(ext[:]))
	}

	if payLen > 1<<20 {
		return nil, errors.New("websocket frame payload too large (> 1 MB)")
	}

	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(br, mask[:]); err != nil {
			return nil, err
		}
	}

	payload := make([]byte, payLen)
	if _, err := io.ReadFull(br, payload); err != nil {
		return nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return &wsFrame{fin: fin, opcode: opcode, payload: payload}, nil
}

// wsWrite sends an unmasked server→client frame.
func wsWrite(conn net.Conn, opcode byte, payload []byte) error {
	b0 := byte(0x80) | opcode // FIN=1, RSV=0
	n := len(payload)
	var hdr []byte
	switch {
	case n <= 125:
		hdr = []byte{b0, byte(n)}
	case n <= 65535:
		hdr = make([]byte, 4)
		hdr[0] = b0
		hdr[1] = 126
		binary.BigEndian.PutUint16(hdr[2:], uint16(n))
	default:
		hdr = make([]byte, 10)
		hdr[0] = b0
		hdr[1] = 127
		binary.BigEndian.PutUint64(hdr[2:], uint64(n))
	}
	_, err := conn.Write(append(hdr, payload...))
	return err
}

func wsWriteClose(conn net.Conn, code uint16, reason string) {
	payload := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(payload, code)
	copy(payload[2:], reason)
	wsWrite(conn, wsOpClose, payload) //nolint:errcheck
}

// wsCloseCode extracts the status code from a close frame payload.
func wsCloseCode(payload []byte) uint16 {
	if len(payload) < 2 {
		return 1000
	}
	return binary.BigEndian.Uint16(payload[:2])
}
