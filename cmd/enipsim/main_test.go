// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/cB-Sigma/enip"
	"github.com/cB-Sigma/enip-sim/sim"
)

func TestRunner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan *sim.Server, 1)
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, []string{"-addr", "127.0.0.1:0", "-slot", "2"}, &out, func(s *sim.Server) { got <- s })
	}()
	var s *sim.Server
	select {
	case s = <-got:
	case err := <-done:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("not ready")
	}
	route, _ := enip.Route("1,2")
	c := &enip.Client{Addr: s.Addr(), Route: route, Timeout: time.Second}
	ty, data, err := c.ReadTag(ctx, "Program:Main.Count", 1)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := enip.Decode(ty, data, 1, nil); v != 42.0 {
		t.Errorf("Count = %v", v)
	}
	c.Close()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRunnerFlags(t *testing.T) {
	for _, args := range [][]string{{"-slot", "300"}, {"-addr", "nowhere"}, {"-bogus"}} {
		if err := run(context.Background(), args, &bytes.Buffer{}, nil); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}
