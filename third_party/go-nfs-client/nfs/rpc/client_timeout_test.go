// Copyright © 2017 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause
//
package rpc

import (
	"errors"
	"net"
	"os"
	"testing"
	"time"
)

type nullCall struct{ Header }

// silentPeer accepts one TCP connection and holds it open without ever
// responding — the silently-dead-TCP failure mode.
func silentPeer(t *testing.T) (addr string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	t.Cleanup(func() { close(done); ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				<-done
				conn.Close()
			}()
		}
	}()
	return ln.Addr().String()
}

// TestCallTimeout: with SetTimeout armed, a Call against a silent peer must
// fail promptly with an errors.Is-recognizable timeout instead of blocking
// forever.
func TestCallTimeout(t *testing.T) {
	client, err := DialTCP("tcp", nil, silentPeer(t))
	if err != nil {
		t.Fatalf("DialTCP: %v", err)
	}
	defer client.Close()
	client.SetTimeout(100 * time.Millisecond)

	start := time.Now()
	_, err = client.Call(&nullCall{Header{
		Rpcvers: 2,
		Prog:    100000,
		Vers:    2,
		Proc:    0,
		Cred:    AuthNull,
		Verf:    AuthNull,
	}})
	el := time.Since(start)
	if err == nil {
		t.Fatal("Call to a silent peer: got nil error, want timeout")
	}
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("Call error %v is not recognizable via errors.Is(os.ErrDeadlineExceeded)", err)
	}
	if el > 5*time.Second {
		t.Fatalf("Call took %v to fail; timeout was 100ms", el)
	}
}

// TestDialTCPPresetsTimeout covers the handshake hole: DialTCP arms the IO
// timeout itself (from DialTimeout), so RPCs issued before the caller could
// call SetTimeout — portmap GETPORT, MNT, FSINFO — cannot block forever on a
// peer that accepts TCP but never answers.
func TestDialTCPPresetsTimeout(t *testing.T) {
	prev := DialTimeout
	DialTimeout = 200 * time.Millisecond
	defer func() { DialTimeout = prev }()

	client, err := DialTCP("tcp", nil, silentPeer(t))
	if err != nil {
		t.Fatalf("DialTCP: %v", err)
	}
	defer client.Close()
	// No SetTimeout call: the preset from DialTCP must bound the Call.

	start := time.Now()
	_, err = client.Call(&nullCall{Header{
		Rpcvers: 2,
		Prog:    100000,
		Vers:    2,
		Proc:    0,
		Cred:    AuthNull,
		Verf:    AuthNull,
	}})
	el := time.Since(start)
	if err == nil {
		t.Fatal("Call to a silent peer without SetTimeout: got nil error, want preset timeout")
	}
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("Call error %v is not recognizable via errors.Is(os.ErrDeadlineExceeded)", err)
	}
	if el > 5*time.Second {
		t.Fatalf("Call took %v to fail; preset DialTimeout was 200ms", el)
	}
}

// TestDialTimeout dials a blackhole address: the connect must fail (or be
// aborted by DialTimeout) promptly instead of riding the OS's full SYN retry
// budget.
func TestDialTimeout(t *testing.T) {
	prev := DialTimeout
	DialTimeout = 200 * time.Millisecond
	defer func() { DialTimeout = prev }()

	start := time.Now()
	// 192.0.2.0/24 (TEST-NET-1, RFC 5737) is unroutable by definition.
	client, err := DialTCP("tcp", nil, "192.0.2.1:2049")
	el := time.Since(start)
	if err == nil {
		client.Close()
		t.Fatal("dial to a blackhole address: got nil error, want failure")
	}
	if el > 5*time.Second {
		t.Fatalf("dial took %v to fail; DialTimeout was 200ms", el)
	}
}
