// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package enip_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cB-Sigma/enip"
	enipsim "github.com/cB-Sigma/enip-sim/sim"
)

func startSim(t *testing.T, routed bool) *enipsim.Server {
	t.Helper()
	s := enipsim.New()
	if routed {
		s.Slot = 2
		s.RequireRoute = true
	}
	if err := s.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	return s
}

func bigValues() []any {
	v := make([]any, 300)
	for i := range v {
		v[i] = float64(i)
	}
	return v
}

// Covers: SIG-IPE-333
func TestENIPClientFragmentedAndBatched(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []struct {
		name      string
		connected bool
		route     string
	}{{"connected routed", true, "1,2"}, {"unconnected routed", false, "1,2"}, {"connected direct", true, ""}, {"unconnected direct", false, ""}} {
		t.Run(mode.name, func(t *testing.T) {
			sim := startSim(t, mode.route != "")
			route, _ := enip.Route(mode.route)
			c := &enip.Client{Addr: sim.Addr(), Route: route, Timeout: time.Second, Connected: mode.connected}
			defer c.Close()
			// DINT[300] = 1200 bytes, more than one reply: fragmented read.
			ty, data, err := c.ReadTag(ctx, "Big", 300)
			if err != nil {
				t.Fatal(err)
			}
			v, _ := enip.Decode(ty, data, 300, nil)
			if !reflect.DeepEqual(v, bigValues()) {
				t.Errorf("Big = %v…", fmt.Sprint(v)[:40])
			}
			if st := sim.Stats(); st.FragmentedReads < 2 {
				t.Errorf("fragmented reads = %d, want at least 2", st.FragmentedReads)
			}
			if m, _ := c.Mode(); (m == "connected") != mode.connected {
				t.Errorf("mode = %s", m)
			}
			// A fragmented write of new values, read back.
			nv := make([]byte, 1200)
			for i := range 300 {
				binary.LittleEndian.PutUint32(nv[4*i:], uint32(1000+i))
			}
			if err := c.WriteTag(ctx, "Big", ty, 300, nv); err != nil {
				t.Fatal(err)
			}
			if st := sim.Stats(); st.FragmentedWrites < 3 {
				t.Errorf("fragmented writes = %d, want at least 3", st.FragmentedWrites)
			}
			if got := sim.TagData("Big"); string(got) != string(nv) {
				t.Error("Big was not written completely")
			}
			// One array element, by index.
			_, d, err := c.ReadTag(ctx, "Big[299]", 1)
			if err != nil || binary.LittleEndian.Uint32(d) != 1299 {
				t.Errorf("Big[299] = % x, %v", d, err)
			}
			// A batch in one Multiple Service Packet, with one unknown tag.
			reads := []*enip.TagRead{{Tag: "Flow", Elements: 1}, {Tag: "Program:Main.Count", Elements: 1}, {Tag: "Nope", Elements: 1}, {Tag: "Name", Elements: 1}}
			before := sim.Stats().Multiple
			if err := c.ReadTags(ctx, reads); err != nil {
				t.Fatal(err)
			}
			if sim.Stats().Multiple != before+1 {
				t.Error("the batch was not one Multiple Service Packet")
			}
			if f, _ := enip.Decode(reads[0].Type, reads[0].Data, 1, nil); f != 12.5 || reads[0].Err != nil {
				t.Errorf("Flow = %v, %v", f, reads[0].Err)
			}
			if n, _ := enip.Decode(reads[1].Type, reads[1].Data, 1, nil); n != 42.0 {
				t.Errorf("Count = %v, %v", n, reads[1].Err)
			}
			if reads[2].Err == nil || !strings.Contains(reads[2].Err.Error(), "path destination unknown") {
				t.Errorf("unknown tag error = %v", reads[2].Err)
			}
			if s, _ := enip.Decode(reads[3].Type, reads[3].Data, 1, nil); s != "Pump 7" {
				t.Errorf("Name = %v, %v", s, reads[3].Err)
			}
			// Identity attributes: single and all.
			b, err := c.GetAttribute(ctx, 1, 1, 7)
			if err != nil || string(b[1:]) != "1756-L83E/B Sim" {
				t.Errorf("product name = %q, %v", b, err)
			}
			all, err := c.GetAttribute(ctx, 1, 1, -1)
			if err != nil || len(all) < 15 || binary.LittleEndian.Uint32(all[10:]) != 0x00C0FFEE {
				t.Errorf("identity get all = % x, %v", all, err)
			}
			if err := c.SetAttribute(ctx, 1, 1, 1, []byte{1, 0}); err == nil || !strings.Contains(err.Error(), "not settable") {
				t.Errorf("setting the vendor ID = %v", err)
			}
			id, err := c.ListIdentity(ctx)
			if err != nil || id.ProductName != "1756-L83E/B Sim" || id.Serial != "00C0FFEE" {
				t.Errorf("list identity = %+v, %v", id, err)
			}
			c.Close()
			if mode.connected {
				eventually(t, 2*time.Second, "forward close", func() bool { return sim.Stats().ForwardCloses == 1 })
			}
		})
	}
	// A route to an empty slot is refused with a readable reason.
	sim := startSim(t, true)
	route, _ := enip.Route("1,5")
	c := &enip.Client{Addr: sim.Addr(), Route: route, Timeout: time.Second}
	defer c.Close()
	if _, _, err := c.ReadTag(ctx, "Flow", 1); err == nil || !strings.Contains(err.Error(), "invalid link address") {
		t.Errorf("wrong slot = %v", err)
	}
	// Without a route, a ControlLogix Ethernet module knows no tags.
	c2 := &enip.Client{Addr: sim.Addr(), Timeout: time.Second}
	defer c2.Close()
	if _, _, err := c2.ReadTag(ctx, "Flow", 1); !enip.IsCIP(err) {
		t.Errorf("unrouted read = %v, want a CIP error", err)
	}
}

// eventually polls cond until it holds or the wait runs out.
func eventually(t *testing.T, wait time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
