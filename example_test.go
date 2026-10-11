// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package enip_test

import (
	"context"
	"fmt"
	"time"

	"github.com/cB-Sigma/enip"
	"github.com/cB-Sigma/enip-sim/sim"
)

// Read a Logix tag through the backplane route to the CPU in slot 2.
func Example() {
	plc := sim.New()
	plc.Slot, plc.RequireRoute = 2, true
	if err := plc.Start("127.0.0.1:0"); err != nil {
		panic(err)
	}
	defer plc.Stop()

	route, _ := enip.Route("1,2")
	c := &enip.Client{Addr: plc.Addr(), Route: route, Timeout: time.Second, Connected: true}
	defer c.Close()
	ty, data, err := c.ReadTag(context.Background(), "Flow", 1)
	if err != nil {
		panic(err)
	}
	v, _ := enip.Decode(ty, data, 1, nil)
	fmt.Println(ty.Name(), v)
	// Output: REAL 12.5
}
