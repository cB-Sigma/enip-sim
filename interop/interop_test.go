// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

// Package interop runs pycomm3's LogixDriver and CIPDriver
// (peer/client.py, in a container) against the simulated Logix
// controller: identity, the controller's info and name, Large and
// standard Forward Open, Read/Write Tag of atomic types, arrays and
// STRING, fragmented reads and writes, Multiple Service Packets, generic
// messages and routing to a CPU slot. The tests need Docker and are
// skipped unless SIGMA_ENIP_SIM_INTEROP=1.
package interop

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cB-Sigma/enip"
	"github.com/cB-Sigma/enip-sim/sim"
)

// event splits a peer line "<step> <json>" and decodes the JSON.
func event(t *testing.T, line string) any {
	t.Helper()
	_, js, _ := strings.Cut(line, " ")
	var v any
	if err := json.Unmarshal([]byte(js), &v); err != nil {
		t.Fatalf("peer line %q: %v", line, err)
	}
	return v
}

// tagOK checks one pycomm3 Tag result (as client.py prints it).
func tagOK(t *testing.T, step string, v any, value any, typ string) {
	t.Helper()
	m, _ := v.(map[string]any)
	if m == nil {
		t.Errorf("%s: %v", step, v)
		return
	}
	if m["error"] != nil {
		t.Errorf("%s: pycomm3 error %v", step, m["error"])
		return
	}
	if typ != "" && m["type"] != typ {
		t.Errorf("%s: type %v, want %s", step, m["type"], typ)
	}
	if value != nil {
		got, _ := json.Marshal(m["value"])
		want, _ := json.Marshal(value)
		if string(got) != string(want) {
			t.Errorf("%s: value %s, want %s", step, got, want)
		}
	}
}

func list(v any) []any { l, _ := v.([]any); return l }

func obj(v any) map[string]any { m, _ := v.(map[string]any); return m }

// run starts the simulator as configured, runs client.py's logix session
// against it and checks every step.
func run(t *testing.T, s *sim.Server, route, size string) {
	if err := s.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	_, port, _ := net.SplitHostPort(s.Addr())
	path := "host.docker.internal:" + port + route
	p := startPeer(t, "--add-host=host.docker.internal:host-gateway", image,
		"python", "-u", "client.py", "logix", path, size, strconv.Itoa(int(s.Slot)))
	le := binary.LittleEndian

	id := obj(event(t, p.expect(60*time.Second, "list_identity")))
	if id["product_name"] != "1756-L83E/B Sim" || id["serial"] != "00c0ffee" {
		t.Errorf("list_identity: %v", id)
	}

	open := obj(event(t, p.expect(20*time.Second, "open")))
	info := obj(open["info"])
	if open["connected"] != true || open["name"] != "SimController" || info["product_name"] != "1756-L83E/B Sim" ||
		obj(info["revision"])["major"] != 33.0 {
		t.Errorf("open: %v", open)
	}
	wantExt, wantSize := size == "large", 4000.0
	if !wantExt {
		wantSize = 500
	}
	if open["extended"] != wantExt || open["connection_size"] != wantSize {
		t.Errorf("open: extended forward open %v, size %v; want %v, %v", open["extended"], open["connection_size"], wantExt, wantSize)
	}

	ra := list(event(t, p.expect(10*time.Second, "read_atomic")))
	if len(ra) != 4 {
		t.Fatalf("read_atomic: %v", ra)
	}
	tagOK(t, "read Flow", ra[0], 12.5, "REAL")
	tagOK(t, "read Program:Main.Count", ra[1], 42, "DINT")
	tagOK(t, "read Running", ra[2], true, "BOOL")
	tagOK(t, "read Total", ra[3], 1e10, "LREAL")

	for i, w := range list(event(t, p.expect(10*time.Second, "write_atomic"))) {
		tagOK(t, "write_atomic "+strconv.Itoa(i), w, nil, "")
	}
	if got := math.Float32frombits(le.Uint32(s.TagData("Flow"))); got != 3.25 {
		t.Errorf("Flow in the simulator after pycomm3's write: %v", got)
	}
	if got := int32(le.Uint32(s.TagData("Program:Main.Count"))); got != -7 {
		t.Errorf("Count in the simulator after pycomm3's write: %v", got)
	}
	if got := math.Float64frombits(le.Uint64(s.TagData("Total"))); got != 2.5 {
		t.Errorf("Total in the simulator after pycomm3's write: %v", got)
	}
	rb := list(event(t, p.expect(10*time.Second, "read_back")))
	if len(rb) != 3 {
		t.Fatalf("read_back: %v", rb)
	}
	tagOK(t, "read back Flow", rb[0], 3.25, "REAL")
	tagOK(t, "read back Count", rb[1], -7, "DINT")
	tagOK(t, "read back Total", rb[2], 2.5, "LREAL")
	if ro := obj(event(t, p.expect(10*time.Second, "write_readonly"))); ro["error"] == nil {
		t.Errorf("pycomm3 wrote the read-only tag Running: %v", ro)
	}

	tagOK(t, "read Levels{10}", event(t, p.expect(10*time.Second, "read_array")), []int{0, 10, 20, 30, 40, 50, 60, 70, 80, 90}, "INT[10]")
	tagOK(t, "write Levels{10}", event(t, p.expect(10*time.Second, "write_array")), nil, "")
	lv := s.TagData("Levels")
	for i := range 10 {
		if got := int(le.Uint16(lv[2*i:])); got != i*3 {
			t.Errorf("Levels[%d] in the simulator: %d, want %d", i, got, i*3)
		}
	}
	tagOK(t, "read Levels[4]", event(t, p.expect(10*time.Second, "read_element")), 12, "INT")

	tagOK(t, "read Name", event(t, p.expect(10*time.Second, "read_string")), "Pump 7", "STRING")
	tagOK(t, "write Name", event(t, p.expect(10*time.Second, "write_string")), nil, "")
	if got, err := enip.DecodeString(s.TagData("Name")); err != nil || got != "Hello pycomm3" {
		t.Errorf("Name in the simulator after pycomm3's write: %q %v", got, err)
	}
	tagOK(t, "read Name again", event(t, p.expect(10*time.Second, "read_string2")), "Hello pycomm3", "STRING")

	if rb := obj(event(t, p.expect(10*time.Second, "read_big"))); rb["error"] != nil || rb["n"] != 300.0 || rb["ok"] != true {
		t.Errorf("read Big{300}: %v", rb)
	}
	tagOK(t, "write Big{300}", event(t, p.expect(10*time.Second, "write_big")), nil, "")
	big := s.TagData("Big")
	for i := range 300 {
		if got := int(le.Uint32(big[4*i:])); got != i*2+1 {
			t.Fatalf("Big[%d] in the simulator after pycomm3's write: %d, want %d", i, got, i*2+1)
		}
	}
	if rb := obj(event(t, p.expect(10*time.Second, "read_big2"))); rb["error"] != nil || rb["ok"] != true {
		t.Errorf("read Big{300} after the write: %v", rb)
	}

	multi := list(event(t, p.expect(10*time.Second, "read_multi")))
	if len(multi) != 5 {
		t.Fatalf("read_multi: %v", multi)
	}
	tagOK(t, "multi Flow", multi[0], 3.25, "REAL")
	tagOK(t, "multi Count", multi[1], -7, "DINT")
	tagOK(t, "multi Levels", multi[2], []int{0, 3, 6, 9, 12, 15, 18, 21, 24, 27}, "INT[10]")
	tagOK(t, "multi Total", multi[3], 2.5, "LREAL")
	tagOK(t, "multi Name", multi[4], "Hello pycomm3", "STRING")

	tagOK(t, "generic unconnected", event(t, p.expect(10*time.Second, "generic_unconnected")), "1756-L83E/B Sim", "")
	tagOK(t, "generic connected", event(t, p.expect(10*time.Second, "generic_connected")), 1, "")
	if mi := obj(event(t, p.expect(10*time.Second, "module_info"))); mi["product_name"] != "1756-L83E/B Sim" {
		t.Errorf("module_info: %v", mi)
	}
	p.expect(10*time.Second, "done")

	// The peer runs ahead of these checks, so the counters are checked
	// once it is done. Only Big (1200 bytes) needs fragments over a
	// 500-byte connection; two reads of several tags (read_back,
	// read_multi) are Multiple Service Packets.
	st := s.Stats()
	if st.ForwardOpens == 0 || st.ForwardCloses == 0 || st.Connected == 0 || st.UnconnectedSends == 0 || st.Multiple < 2 {
		t.Errorf("simulator counters %+v", st)
	}
	if size == "small" && (st.FragmentedReads == 0 || st.FragmentedWrites == 0) {
		t.Errorf("pycomm3 moved Big without fragments over a 500-byte connection: %+v", st)
	}
	if size == "large" && (st.FragmentedReads != 0 || st.FragmentedWrites != 0) {
		t.Errorf("pycomm3 used fragments over a 4000-byte connection: %+v", st)
	}
}

// pycomm3 against the simulator answering without a route (CompactLogix
// style; pycomm3 still routes to backplane slot 0), over a Large Forward
// Open connection of 4000 bytes and a standard one of 500.
func TestPycomm3AgainstSimulator(t *testing.T) {
	needDocker(t)
	for _, size := range []string{"large", "small"} {
		t.Run(size, func(t *testing.T) {
			run(t, sim.New(), "", size)
		})
	}
}

// pycomm3 against the simulator as a ControlLogix chassis whose CPU is in
// slot 2 and which refuses unrouted tag requests: the path names the
// route 1/2.
func TestPycomm3AgainstRoutedSimulator(t *testing.T) {
	needDocker(t)
	for _, size := range []string{"large", "small"} {
		t.Run(size, func(t *testing.T) {
			s := sim.New()
			s.Slot, s.RequireRoute = 2, true
			run(t, s, "/1/2", size)
		})
	}
}

// pycomm3's discover (ListIdentity over UDP) reaches the simulator. It
// sends to port 44818, so the simulator must listen there; the test is
// skipped when the port is taken.
func TestPycomm3DiscoversSimulator(t *testing.T) {
	needDocker(t)
	s := sim.New()
	if err := s.Start("127.0.0.1:44818"); err != nil {
		t.Skipf("port 44818 is not free on this host: %v", err)
	}
	defer s.Stop()
	p := startPeer(t, "--add-host=host.docker.internal:host-gateway", image,
		"python", "-u", "client.py", "discover", "host.docker.internal")
	found := list(event(t, p.expect(60*time.Second, "discover")))
	if len(found) == 0 {
		t.Fatalf("pycomm3 discovered nothing (simulator saw %d UDP ListIdentity)", s.Stats().ListIdentityUDP)
	}
	if id := obj(found[0]); id["product_name"] != "1756-L83E/B Sim" {
		t.Errorf("discovered %v", id)
	}
}
