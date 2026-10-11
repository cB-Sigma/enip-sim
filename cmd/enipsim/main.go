// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

// Command enipsim serves a simulated Logix controller over EtherNet/IP, so
// a client can be tried without a PLC. It answers on TCP and UDP at -addr
// (ListIdentity on both), with a demo tag table (Flow, Program:Main.Count,
// Running, Levels, Name, Recipe, Big) and the identity object. With -slot
// the CPU sits in that backplane slot and requests must carry the route
// (for example "1,2").
//
//	enipsim -addr 127.0.0.1:44818 -slot 2
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/cB-Sigma/enip-sim/sim"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, nil); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "enipsim:", err)
		os.Exit(1)
	}
}

// run serves until ctx ends. ready, when not nil, receives the simulator
// once it listens.
func run(ctx context.Context, args []string, out io.Writer, ready func(*sim.Server)) error {
	fs := flag.NewFlagSet("enipsim", flag.ContinueOnError)
	fs.SetOutput(out)
	addr := fs.String("addr", "127.0.0.1:44818", "TCP and UDP address to listen on")
	slot := fs.Int("slot", -1, "backplane slot of the CPU; requests must then carry the route (-1: no routing)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *slot < -1 || *slot > 255 {
		return fmt.Errorf("-slot %d is out of range", *slot)
	}
	s := sim.New()
	if *slot >= 0 {
		s.Slot = byte(*slot)
		s.RequireRoute = true
	}
	if err := s.Start(*addr); err != nil {
		return err
	}
	defer s.Stop()
	fmt.Fprintf(out, "enipsim: Logix controller on tcp and udp %s\n", s.Addr())
	if *slot >= 0 {
		fmt.Fprintf(out, "enipsim: CPU in slot %d (route \"1,%d\")\n", *slot, *slot)
	}
	if ready != nil {
		ready(s)
	}
	<-ctx.Done()
	return nil
}
