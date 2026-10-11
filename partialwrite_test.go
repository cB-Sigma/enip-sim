package enip_test

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/cB-Sigma/enip"
)

// General status 0x06 (partial transfer) only means more data follows on
// a read. A device that answers a Write Tag or Set Attribute Single with
// it did not take the write, and the write fails.
//
// Covers: SIG-CMD-215
func TestENIPWriteAnsweredPartialFails(t *testing.T) {
	ctx := context.Background()
	sim := startSim(t, false)
	c := &enip.Client{Addr: sim.Addr(), Timeout: time.Second}
	defer c.Close()
	sim.AnswerWritesWith(enip.StatusPartial)
	v := make([]byte, 4)
	binary.LittleEndian.PutUint32(v, 7)
	if err := c.WriteTag(ctx, "Program:Main.Count", enip.Type{Code: enip.TypeDINT}, 1, v); err == nil {
		t.Error("a Write Tag answered 0x06 was reported as written")
	}
	if err := c.SetAttribute(ctx, 0x64, 1, 1, v); err == nil {
		t.Error("a Set Attribute Single answered 0x06 was reported as written")
	}
	sim.AnswerWritesWith(0)
	if err := c.WriteTag(ctx, "Program:Main.Count", enip.Type{Code: enip.TypeDINT}, 1, v); err != nil {
		t.Errorf("a normal write: %v", err)
	}
}
