// Copyright © 2017 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause
//
package nfs

import (
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/vmware/go-nfs-client/nfs/rpc"
)

// TestMountTimeoutOnSilentPeer: the MNT handshake RPC runs before the
// embedding application could arm any timeout of its own. With DialTCP's
// preset (rpc.DialTimeout) the Mount call must fail with a recognizable
// timeout against a peer that accepts TCP but never answers — the hole that
// previously blocked pool dialing (or leaked a re-dial goroutine) forever.
func TestMountTimeoutOnSilentPeer(t *testing.T) {
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

	prev := rpc.DialTimeout
	rpc.DialTimeout = 200 * time.Millisecond
	defer func() { rpc.DialTimeout = prev }()

	client, err := rpc.DialTCP("tcp", nil, ln.Addr().String())
	if err != nil {
		t.Fatalf("DialTCP: %v", err)
	}
	defer client.Close()

	m := &Mount{Client: client, Addr: "127.0.0.1"}
	start := time.Now()
	_, err = m.Mount("/export", rpc.AuthNull)
	el := time.Since(start)
	if err == nil {
		t.Fatal("Mount against a silent peer: got nil error, want timeout")
	}
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("Mount error %v is not recognizable via errors.Is(os.ErrDeadlineExceeded)", err)
	}
	if el > 5*time.Second {
		t.Fatalf("Mount took %v to fail; preset DialTimeout was 200ms", el)
	}
}
