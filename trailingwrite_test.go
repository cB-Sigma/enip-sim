// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package enip_test

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/cB-Sigma/enip"
	enipsim "github.com/cB-Sigma/enip-sim/sim"
)

// A Write Tag whose data runs past the elements it names writes those
// elements and ignores the rest: pycomm3 1.2.16 sends a single-tag write
// over a connection as sequence count + request, twice. The request below
// is the one it sent for Levels{10} = 0, 3, …, 27 (with its trailing copy).
// A short value is still refused.
func TestWriteTagIgnoresTrailingBytes(t *testing.T) {
	ctx := context.Background()
	sim := startSim(t, false)
	c := &enip.Client{Addr: sim.Addr(), Timeout: time.Second, Connected: true}
	defer c.Close()
	path, _ := enip.TagPath("Levels")
	var val []byte
	for i := range 10 {
		val = binary.LittleEndian.AppendUint16(val, uint16(i*3))
	}
	data := append([]byte{0xC3, 0x00, 10, 0}, val...)
	// what pycomm3 appends: its sequence count and the request again
	trailing := append([]byte{0x02, 0x00}, enip.Request(enip.SvcWriteTag, path, data)...)
	r, err := c.Do(ctx, enip.SvcWriteTag, path, append(append([]byte(nil), data...), trailing...))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.WriteErr(); err != nil {
		t.Fatalf("Write Tag with trailing bytes: %v", err)
	}
	got := sim.TagData("Levels")
	for i := range 10 {
		if v := binary.LittleEndian.Uint16(got[2*i:]); int(v) != i*3 {
			t.Fatalf("Levels[%d] = %d, want %d", i, v, i*3)
		}
	}

	r, err = c.Do(ctx, enip.SvcWriteTag, path, data[:len(data)-2])
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != enip.StatusNotEnoughData {
		t.Errorf("a short Write Tag: status 0x%02X, want 0x%02X", r.Status, enip.StatusNotEnoughData)
	}
}

// Get_Attributes_All on class 0x64 instance 1 answers the controller name
// as a CIP STRING; pycomm3's LogixDriver reads it on open and fails to
// connect without it. Attribute 1 of that instance stays the settable DINT.
func TestControllerNameAnswered(t *testing.T) {
	ctx := context.Background()
	sim := enipsim.New()
	sim.Slot, sim.RequireRoute, sim.ControllerName = 2, true, "Line_3"
	if err := sim.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sim.Stop)
	route, _ := enip.Route("1,2")
	c := &enip.Client{Addr: sim.Addr(), Timeout: time.Second, Route: route}
	defer c.Close()
	got, err := c.GetAttribute(ctx, 0x64, 1, -1)
	if err != nil {
		t.Fatal(err)
	}
	if want := append([]byte{6, 0}, "Line_3"...); string(got) != string(want) {
		t.Errorf("controller name %x, want %x", got, want)
	}
	v, err := c.GetAttribute(ctx, 0x64, 1, 1)
	if err != nil || binary.LittleEndian.Uint32(v) != 5 {
		t.Errorf("attribute 1: %x %v", v, err)
	}
}
