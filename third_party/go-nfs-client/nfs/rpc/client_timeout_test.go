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

// TestCallTimeout hangs a Call on a peer that accepts the connection but
// never replies (the silently-dead-TCP failure mode): with SetTimeout armed
// the recv must fail promptly with an errors.Is-recognizable timeout instead
// of blocking forever.
func TestCallTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		<-done // hold the connection open without ever responding
		conn.Close()
	}()

	client, err := DialTCP("tcp", nil, ln.Addr().String())
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
