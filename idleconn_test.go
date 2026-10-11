package enip_test

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/cB-Sigma/enip"
)

// A class 3 connection left idle longer than its own timeout is dropped
// by the target. The client opens a new one before using it instead of
// failing the first write after a long pause.
//
// Covers: SIG-CMD-247
func TestENIPWriteAfterConnectionTimedOut(t *testing.T) {
	ctx := context.Background()
	sim := startSim(t, false)
	// KeepAlive 1 s asks for the shortest timeout: RPI 2 s × 4 = 8 s.
	c := &enip.Client{Addr: sim.Addr(), Timeout: time.Second, Connected: true, KeepAlive: time.Second}
	defer c.Close()
	v := make([]byte, 4)
	binary.LittleEndian.PutUint32(v, 7)
	if err := c.WriteTag(ctx, "Program:Main.Count", enip.Type{Code: enip.TypeDINT}, 1, v); err != nil {
		t.Fatal(err)
	}
	time.Sleep(9 * time.Second)
	binary.LittleEndian.PutUint32(v, 8)
	err := c.WriteTag(ctx, "Program:Main.Count", enip.Type{Code: enip.TypeDINT}, 1, v)
	if err != nil {
		t.Errorf("write after the connection timed out: %v", err)
	}
	if m, _ := c.Mode(); m != "connected" {
		t.Errorf("mode %s", m)
	}
}
