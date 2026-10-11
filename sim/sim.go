// Package sim is an in-process EtherNet/IP adapter that behaves like a
// Logix controller for tests: a tag table, the identity object, sessions,
// Unconnected Send routing to a CPU slot, class 3 connections (Forward
// Open/Close and SendUnitData), Multiple Service Packets, Read/Write Tag
// with fragmentation, attribute services and ListIdentity over TCP and UDP.
package sim

import (
	"encoding/binary"
	"errors"
	"math"
	"math/rand/v2"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/cB-Sigma/enip"
)

// Tag is one simulated controller tag.
type Tag struct {
	Type     enip.Type
	Elements int
	Data     []byte
	ReadOnly bool // writes get a privilege violation
}

// Attr is one simulated object attribute.
type Attr struct {
	Data     []byte
	Settable bool
}

// AttrKey addresses an attribute.
type AttrKey struct {
	Class, Instance uint16
	Attribute       int
}

// Stats counts what the simulator saw.
type Stats struct {
	Sessions, ForwardOpens, ForwardCloses, UnconnectedSends, Direct, Connected int
	Multiple, Reads, FragmentedReads, Writes, FragmentedWrites                 int
	ListIdentityTCP, ListIdentityUDP                                           int
}

type connection struct {
	ot, to uint32
	serial uint16
	size   int
	// timeout is the connection's inactivity timeout (O→T RPI × 4 <<
	// timeout multiplier); last is when it last carried a message. A
	// target drops a class 3 connection idle longer than its timeout.
	timeout time.Duration
	last    time.Time
}

// Server is the simulated adapter.
type Server struct {
	// Slot is the CPU slot on backplane port 1 that routed requests must
	// name. RequireRoute refuses unrouted tag requests (like a
	// ControlLogix Ethernet module); without it the adapter also answers
	// directly (like a CompactLogix).
	Slot         byte
	RequireRoute bool
	// ControllerName is the controller (project) name a Logix controller
	// answers to Get_Attributes_All on class 0x64 instance 1 (a CIP
	// STRING); clients such as pycomm3 read it when they connect.
	ControllerName string

	mu       sync.Mutex
	tags     map[string]*Tag
	attrs    map[AttrKey]*Attr
	refuseFO bool
	silent   bool
	// writeStatus, when set, answers every write (Write Tag, Set
	// Attribute Single) with this general status and applies nothing.
	writeStatus byte
	stats       Stats
	conns       map[uint32]*connection
	addr        string
	ln          net.Listener
	udp         *net.UDPConn
	open        map[net.Conn]bool
	wg          sync.WaitGroup
	identity    enip.Identity
	serialNum   uint32
}

// AnswerWritesWith makes the device answer every write with general
// status st without applying it (0 writes normally again).
func (s *Server) AnswerWritesWith(st byte) {
	s.mu.Lock()
	s.writeStatus = st
	s.mu.Unlock()
}

// New returns a simulator with a default tag table:
//
//	Flow REAL 12.5, Program:Main.Count DINT 42, Running BOOL true (read-only),
//	Levels INT[10] (0,10,…,90), Name STRING "Pump 7",
//	Recipe (structure 0x1234, 12 bytes: DINT 7, REAL 1.5, INT 20),
//	Big DINT[300] (i), Total LREAL 1e10.
//
// and the identity object (class 1) plus a settable DINT at class 0x64
// instance 1 attribute 1 (value 5); Get_Attributes_All on that instance
// answers ControllerName ("SimController").
func New() *Server {
	s := &Server{tags: map[string]*Tag{}, attrs: map[AttrKey]*Attr{}, conns: map[uint32]*connection{}, open: map[net.Conn]bool{},
		serialNum: 0x00C0FFEE, ControllerName: "SimController"}
	s.identity = enip.Identity{Protocol: 1, Vendor: 1, DeviceType: 0x0E, ProductCode: 166, ProductName: "1756-L83E/B Sim"}
	le := binary.LittleEndian
	s.SetTag("Flow", enip.Type{Code: enip.TypeREAL}, 1, le.AppendUint32(nil, math.Float32bits(12.5)))
	s.SetTag("Program:Main.Count", enip.Type{Code: enip.TypeDINT}, 1, le.AppendUint32(nil, 42))
	s.SetTag("Running", enip.Type{Code: enip.TypeBOOL}, 1, []byte{1})
	s.tags["Running"].ReadOnly = true
	var lv []byte
	for i := range 10 {
		lv = le.AppendUint16(lv, uint16(i*10))
	}
	s.SetTag("Levels", enip.Type{Code: enip.TypeINT}, 10, lv)
	str, _ := enip.EncodeString("Pump 7")
	s.SetTag("Name", enip.Type{Code: enip.TypeStruct, Handle: enip.HandleSTRING}, 1, str)
	rec := le.AppendUint32(nil, 7)
	rec = le.AppendUint32(rec, math.Float32bits(1.5))
	rec = le.AppendUint16(rec, 20)
	rec = append(rec, 0, 0)
	s.SetTag("Recipe", enip.Type{Code: enip.TypeStruct, Handle: 0x1234}, 1, rec)
	var big []byte
	for i := range 300 {
		big = le.AppendUint32(big, uint32(i))
	}
	s.SetTag("Big", enip.Type{Code: enip.TypeDINT}, 300, big)
	s.SetTag("Total", enip.Type{Code: enip.TypeLREAL}, 1, le.AppendUint64(nil, math.Float64bits(1e10)))
	s.attrs[AttrKey{0x64, 1, 1}] = &Attr{Data: le.AppendUint32(nil, 5), Settable: true}
	return s
}

// SetTag creates or replaces a tag.
func (s *Server) SetTag(name string, t enip.Type, elements int, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tags[name] = &Tag{Type: t, Elements: elements, Data: append([]byte(nil), data...)}
}

// TagData returns a copy of a tag's bytes.
func (s *Server) TagData(name string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.tags[name]; t != nil {
		return append([]byte(nil), t.Data...)
	}
	return nil
}

// AttrData returns a copy of an attribute's bytes.
func (s *Server) AttrData(k AttrKey) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a := s.attrs[k]; a != nil {
		return append([]byte(nil), a.Data...)
	}
	return nil
}

// RefuseForwardOpen makes Forward Open fail (out of connections).
func (s *Server) RefuseForwardOpen(v bool) {
	s.mu.Lock()
	s.refuseFO = v
	s.mu.Unlock()
}

// Silent makes the adapter swallow every request without answering.
func (s *Server) Silent(v bool) {
	s.mu.Lock()
	s.silent = v
	s.mu.Unlock()
}

// Stats returns the counters.
func (s *Server) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// Addr is the TCP (and UDP) address, valid after Start.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}

// Start listens on addr ("127.0.0.1:0" picks a port that is free for both
// TCP and UDP); after Stop, Start("") reuses the previous address.
func (s *Server) Start(addr string) error {
	if addr == "" {
		addr = s.Addr()
	}
	var ln net.Listener
	var udp *net.UDPConn
	var err error
	for try := 0; try < 20; try++ {
		ln, err = net.Listen("tcp4", addr)
		if err != nil {
			return err
		}
		udp, err = net.ListenUDP("udp4", net.UDPAddrFromAddrPort(ln.Addr().(*net.TCPAddr).AddrPort()))
		if err == nil {
			break
		}
		_ = ln.Close()
		if !strings.HasSuffix(addr, ":0") {
			return err
		}
	}
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.ln, s.udp, s.addr = ln, udp, ln.Addr().String()
	s.mu.Unlock()
	s.wg.Add(2)
	go s.accept(ln)
	go s.serveUDP(udp)
	return nil
}

// Stop closes the listeners and every connection and forgets connections.
func (s *Server) Stop() {
	s.mu.Lock()
	ln, udp := s.ln, s.udp
	s.ln, s.udp = nil, nil
	for c := range s.open {
		_ = c.Close()
	}
	s.conns = map[uint32]*connection{}
	s.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
	}
	if udp != nil {
		_ = udp.Close()
	}
	s.wg.Wait()
}

func (s *Server) accept(ln net.Listener) {
	defer s.wg.Done()
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.open[c] = true
		s.mu.Unlock()
		s.wg.Add(1)
		go s.serve(c)
	}
}

func (s *Server) serveUDP(pc *net.UDPConn) {
	defer s.wg.Done()
	buf := make([]byte, 600)
	for {
		n, from, err := pc.ReadFromUDP(buf)
		if err != nil {
			return
		}
		h, err := enip.ParseHeader(buf[:n])
		if err != nil || h.Command != enip.CmdListIdentity {
			continue
		}
		s.mu.Lock()
		silent := s.silent
		s.stats.ListIdentityUDP++
		s.mu.Unlock()
		if silent {
			continue
		}
		_, _ = pc.WriteToUDP(h.Marshal(s.identityBody()), from)
	}
}

func (s *Server) identityBody() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.identity
	host, _, _ := net.SplitHostPort(s.addr)
	id.IP = host
	id.Port = enip.DefaultPort
	return enip.MarshalCPF(enip.Item{Type: enip.ItemIdentity, Data: enip.IdentityItem(id, 33, 11, s.serialNum)})
}

func (s *Server) serve(c net.Conn) {
	defer s.wg.Done()
	defer func() {
		s.mu.Lock()
		delete(s.open, c)
		s.mu.Unlock()
		_ = c.Close()
	}()
	var session uint32
	for {
		h, data, err := enip.ReadPacket(c)
		if err != nil {
			return
		}
		s.mu.Lock()
		silent := s.silent
		s.mu.Unlock()
		if silent {
			continue
		}
		var reply []byte
		switch h.Command {
		case enip.CmdRegisterSession:
			session = rand.Uint32() | 1
			h.Session = session
			s.count(func(st *Stats) { st.Sessions++ })
			reply = data
		case enip.CmdUnRegisterSession:
			return
		case enip.CmdListIdentity:
			s.count(func(st *Stats) { st.ListIdentityTCP++ })
			reply = s.identityBody()
		case enip.CmdSendRRData, enip.CmdSendUnitData:
			if h.Session != session || session == 0 {
				h.Status = 0x64
				break
			}
			reply, err = s.sendData(h.Command, data)
			if err != nil {
				h.Status = 0x03
				reply = nil
			}
		case enip.CmdNOP:
			continue
		default:
			h.Status = 0x01
		}
		if _, err := c.Write(h.Marshal(reply)); err != nil {
			return
		}
	}
}

func (s *Server) count(f func(*Stats)) {
	s.mu.Lock()
	f(&s.stats)
	s.mu.Unlock()
}

func (s *Server) sendData(cmd uint16, body []byte) ([]byte, error) {
	items, err := enip.ParseRRData(body)
	if err != nil {
		return nil, err
	}
	if cmd == enip.CmdSendRRData {
		for _, it := range items {
			if it.Type == enip.ItemUnconnectedData {
				resp := s.handle(it.Data, false, 0)
				return enip.RRData(0, enip.Item{Type: enip.ItemNullAddress}, enip.Item{Type: enip.ItemUnconnectedData, Data: resp}), nil
			}
		}
		return nil, errors.New("no unconnected data item")
	}
	var ot uint32
	var cd []byte
	for _, it := range items {
		switch it.Type {
		case enip.ItemConnectedAddress:
			if len(it.Data) == 4 {
				ot = binary.LittleEndian.Uint32(it.Data)
			}
		case enip.ItemConnectedData:
			cd = it.Data
		}
	}
	s.mu.Lock()
	cn := s.conns[ot]
	if cn != nil && cn.timeout > 0 && time.Since(cn.last) > cn.timeout {
		delete(s.conns, ot) // timed out: the target forgot it
		cn = nil
	}
	if cn != nil {
		cn.last = time.Now()
	}
	s.mu.Unlock()
	if cn == nil || len(cd) < 2 {
		return nil, errors.New("unknown connection")
	}
	s.count(func(st *Stats) { st.Connected++ })
	resp := s.handle(cd[2:], true, cn.size)
	out := append(append([]byte(nil), cd[:2]...), resp...)
	return enip.RRData(0, enip.Item{Type: enip.ItemConnectedAddress, Data: binary.LittleEndian.AppendUint32(nil, cn.to)},
		enip.Item{Type: enip.ItemConnectedData, Data: out}), nil
}

var cmPath = enip.Logical(enip.ClassConnectionManager, 1, -1)
var mrPath = enip.Logical(enip.ClassMessageRouter, 1, -1)

// handle answers one CIP message that arrived unconnected (SendRRData) or
// over a class 3 connection of the given size (SendUnitData).
func (s *Server) handle(msg []byte, connected bool, size int) []byte {
	svc, path, data, err := enip.ParseRequest(msg)
	if err != nil {
		return enip.MarshalResponse(0, enip.StatusNotEnoughData, nil, nil)
	}
	limit := 504
	if connected {
		limit = size - 2
	}
	if string(path) == string(cmPath) {
		switch svc {
		case enip.SvcUnconnectedSend:
			return s.unconnectedSend(data)
		case enip.SvcForwardOpen, enip.SvcLargeForwardOpen:
			return s.forwardOpen(svc, data)
		case enip.SvcForwardClose:
			return s.forwardClose(data)
		}
		return enip.MarshalResponse(svc, enip.StatusServiceNotSup, nil, nil)
	}
	if !connected && s.RequireRoute {
		// The Ethernet module itself has no tags or user objects.
		if len(path) > 0 && path[0] == 0x91 || string(path) == string(mrPath) {
			return enip.MarshalResponse(svc, enip.StatusPathUnknown, nil, nil)
		}
	}
	if !connected {
		s.count(func(st *Stats) { st.Direct++ })
	}
	return s.route(svc, path, data, limit)
}

// routedHandle is the CPU side of an Unconnected Send.
func (s *Server) routedHandle(msg []byte) []byte {
	svc, path, data, err := enip.ParseRequest(msg)
	if err != nil {
		return enip.MarshalResponse(0, enip.StatusNotEnoughData, nil, nil)
	}
	return s.route(svc, path, data, 504)
}

func (s *Server) route(svc byte, path, data []byte, limit int) []byte {
	switch {
	case svc == enip.SvcMultipleService && string(path) == string(mrPath):
		return s.multiple(data, limit)
	case len(path) > 0 && path[0] == 0x91:
		return s.tagService(svc, path, data, limit)
	case len(path) > 0 && path[0]&0xE0 == 0x20:
		return s.attrService(svc, path, data)
	}
	return enip.MarshalResponse(svc, enip.StatusPathSegment, nil, nil)
}

func (s *Server) unconnectedSend(data []byte) []byte {
	s.count(func(st *Stats) { st.UnconnectedSends++ })
	msg, route, err := enip.ParseUnconnectedSend(data)
	if err != nil {
		return enip.MarshalResponse(enip.SvcUnconnectedSend, enip.StatusNotEnoughData, nil, nil)
	}
	if len(route) != 2 || route[0] != 0x01 || route[1] != s.Slot {
		return enip.MarshalResponse(enip.SvcUnconnectedSend, enip.StatusConnFailure, []uint16{0x0312}, []byte{0, 0})
	}
	return s.routedHandle(msg)
}

func (s *Server) forwardOpen(svc byte, d []byte) []byte {
	s.mu.Lock()
	refuse := s.refuseFO
	s.stats.ForwardOpens++
	s.mu.Unlock()
	le := binary.LittleEndian
	paramLen := 2
	if svc == enip.SvcLargeForwardOpen {
		paramLen = 4
	}
	fixed := 2 + 4 + 4 + 2 + 2 + 4 + 4 + 2*(4+paramLen) + 1 + 1
	if len(d) < fixed {
		return enip.MarshalResponse(svc, enip.StatusNotEnoughData, nil, nil)
	}
	ident := d[10:18] // serial, vendor, originator serial
	fail := func(ext uint16) []byte {
		return enip.MarshalResponse(svc, enip.StatusConnFailure, []uint16{ext}, append(append([]byte(nil), ident...), 0, 0))
	}
	if refuse {
		return fail(0x0113)
	}
	var size int
	if paramLen == 2 {
		size = int(le.Uint16(d[22+4:]) & 0x1FF)
	} else {
		size = int(le.Uint32(d[22+4:]) & 0xFFFF)
	}
	path := d[fixed : fixed+int(d[fixed-1])*2]
	want := mrPath
	if s.RequireRoute || len(path) > len(mrPath) {
		want = append([]byte{0x01, s.Slot}, mrPath...)
	}
	if string(path) != string(want) {
		return fail(0x0315)
	}
	rpi := time.Duration(le.Uint32(d[22:])) * time.Microsecond
	cn := &connection{ot: rand.Uint32() | 1, to: le.Uint32(d[6:]), serial: le.Uint16(d[10:]), size: size,
		timeout: rpi * time.Duration(4<<min(d[18], 7)), last: time.Now()}
	s.mu.Lock()
	s.conns[cn.ot] = cn
	s.mu.Unlock()
	out := le.AppendUint32(nil, cn.ot)
	out = le.AppendUint32(out, cn.to)
	out = append(out, ident...)
	out = le.AppendUint32(out, le.Uint32(d[22:]))
	out = le.AppendUint32(out, le.Uint32(d[22+4+paramLen:]))
	out = append(out, 0, 0)
	return enip.MarshalResponse(svc, 0, nil, out)
}

func (s *Server) forwardClose(d []byte) []byte {
	if len(d) < 10 {
		return enip.MarshalResponse(enip.SvcForwardClose, enip.StatusNotEnoughData, nil, nil)
	}
	serial := binary.LittleEndian.Uint16(d[2:])
	s.mu.Lock()
	s.stats.ForwardCloses++
	found := false
	for k, c := range s.conns {
		if c.serial == serial {
			delete(s.conns, k)
			found = true
		}
	}
	s.mu.Unlock()
	if !found {
		return enip.MarshalResponse(enip.SvcForwardClose, enip.StatusConnFailure, []uint16{0x0107}, append(append([]byte(nil), d[2:10]...), 0, 0))
	}
	return enip.MarshalResponse(enip.SvcForwardClose, 0, nil, append(append([]byte(nil), d[2:10]...), 0, 0))
}

func (s *Server) multiple(data []byte, limit int) []byte {
	s.count(func(st *Stats) { st.Multiple++ })
	reqs, err := enip.SplitMultiple(data)
	if err != nil {
		return enip.MarshalResponse(enip.SvcMultipleService, enip.StatusNotEnoughData, nil, nil)
	}
	status := byte(0)
	resps := make([][]byte, len(reqs))
	for i, r := range reqs {
		svc, path, d, err := enip.ParseRequest(r)
		if err != nil {
			resps[i] = enip.MarshalResponse(0, enip.StatusNotEnoughData, nil, nil)
		} else {
			resps[i] = s.route(svc, path, d, limit)
		}
		if resps[i][2] != 0 && resps[i][2] != enip.StatusPartial {
			status = enip.StatusEmbeddedError
		}
	}
	body := enip.MultipleService(resps)
	if 4+len(body) > limit {
		return enip.MarshalResponse(enip.SvcMultipleService, enip.StatusReplyTooLarge, nil, nil)
	}
	return enip.MarshalResponse(enip.SvcMultipleService, status, nil, body)
}

// parseTagPath turns symbolic and element segments back into a tag name
// and an index.
func parseTagPath(p []byte) (string, int, bool) {
	var names []string
	idx := 0
	le := binary.LittleEndian
	for len(p) > 0 {
		switch p[0] {
		case 0x91:
			if len(p) < 2 || len(p) < 2+int(p[1]) {
				return "", 0, false
			}
			n := int(p[1])
			names = append(names, string(p[2:2+n]))
			p = p[2+n+n%2:]
		case 0x28:
			if len(p) < 2 {
				return "", 0, false
			}
			idx, p = int(p[1]), p[2:]
		case 0x29:
			if len(p) < 4 {
				return "", 0, false
			}
			idx, p = int(le.Uint16(p[2:])), p[4:]
		case 0x2A:
			if len(p) < 6 {
				return "", 0, false
			}
			idx, p = int(le.Uint32(p[2:])), p[6:]
		default:
			return "", 0, false
		}
	}
	return strings.Join(names, "."), idx, len(names) > 0
}

func (s *Server) tagService(svc byte, path, d []byte, limit int) []byte {
	name, idx, ok := parseTagPath(path)
	if !ok {
		return enip.MarshalResponse(svc, enip.StatusPathSegment, nil, nil)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tags[name]
	if t == nil {
		return enip.MarshalResponse(svc, enip.StatusPathUnknown, nil, nil)
	}
	le := binary.LittleEndian
	esz := len(t.Data) / t.Elements
	beyond := enip.MarshalResponse(svc, enip.StatusVendor, []uint16{0x2105}, nil)
	switch svc {
	case enip.SvcReadTag, enip.SvcReadTagFragmented:
		if svc == enip.SvcReadTag {
			s.stats.Reads++
		} else {
			s.stats.FragmentedReads++
		}
		if len(d) < 2 || (svc == enip.SvcReadTagFragmented && len(d) < 6) {
			return enip.MarshalResponse(svc, enip.StatusNotEnoughData, nil, nil)
		}
		n := int(le.Uint16(d))
		if n < 1 || idx+n > t.Elements {
			return beyond
		}
		all := t.Data[idx*esz : (idx+n)*esz]
		off := 0
		if svc == enip.SvcReadTagFragmented {
			off = int(le.Uint32(d[2:]))
			if off > len(all) {
				return enip.MarshalResponse(svc, enip.StatusVendor, []uint16{0x2104}, nil)
			}
		}
		tb := t.Type.Marshal()
		room := limit - 4 - len(tb)
		room -= room % 4
		chunk := all[off:]
		status := byte(0)
		if len(chunk) > room {
			chunk, status = chunk[:room], enip.StatusPartial
		}
		return enip.MarshalResponse(svc, status, nil, append(tb, chunk...))
	case enip.SvcWriteTag, enip.SvcWriteTagFragmented:
		if svc == enip.SvcWriteTag {
			s.stats.Writes++
		} else {
			s.stats.FragmentedWrites++
		}
		if s.writeStatus != 0 {
			return enip.MarshalResponse(svc, s.writeStatus, nil, nil)
		}
		wt, rest, err := enip.ParseType(d)
		if err != nil || len(rest) < 2 {
			return enip.MarshalResponse(svc, enip.StatusNotEnoughData, nil, nil)
		}
		if wt != t.Type {
			return enip.MarshalResponse(svc, enip.StatusVendor, []uint16{0x2107}, nil)
		}
		if t.ReadOnly {
			return enip.MarshalResponse(svc, 0x0F, nil, nil)
		}
		n := int(le.Uint16(rest))
		rest = rest[2:]
		if n < 1 || idx+n > t.Elements {
			return beyond
		}
		dst := t.Data[idx*esz : (idx+n)*esz]
		off := 0
		if svc == enip.SvcWriteTagFragmented {
			if len(rest) < 4 {
				return enip.MarshalResponse(svc, enip.StatusNotEnoughData, nil, nil)
			}
			off, rest = int(le.Uint32(rest)), rest[4:]
			if off+len(rest) > len(dst) {
				return enip.MarshalResponse(svc, enip.StatusTooMuchData, nil, nil)
			}
		} else if len(rest) < len(dst) {
			return enip.MarshalResponse(svc, enip.StatusNotEnoughData, nil, nil)
		} else if len(rest) > len(dst) {
			// The element count and type size say how much is written;
			// bytes after that are ignored. pycomm3 1.2.16 sends its
			// single-tag Write Tag over a connection with the whole
			// connected message repeated after the value.
			rest = rest[:len(dst)]
		}
		copy(dst[off:], rest)
		return enip.MarshalResponse(svc, 0, nil, nil)
	}
	return enip.MarshalResponse(svc, enip.StatusServiceNotSup, nil, nil)
}

func parseLogical(p []byte) (class, inst uint16, attr int, ok bool) {
	attr = -1
	le := binary.LittleEndian
	for len(p) >= 2 {
		var v uint32
		seg := p[0]
		switch seg & 0x03 {
		case 0:
			v, p = uint32(p[1]), p[2:]
		case 1:
			if len(p) < 4 {
				return 0, 0, 0, false
			}
			v, p = uint32(le.Uint16(p[2:])), p[4:]
		default:
			return 0, 0, 0, false
		}
		switch seg &^ 0x03 {
		case 0x20:
			class = uint16(v)
		case 0x24:
			inst = uint16(v)
		case 0x30:
			attr = int(v)
		default:
			return 0, 0, 0, false
		}
	}
	return class, inst, attr, len(p) == 0 && class != 0
}

func (s *Server) identityAttr(a int) []byte {
	le := binary.LittleEndian
	id := s.identity
	switch a {
	case 1:
		return le.AppendUint16(nil, id.Vendor)
	case 2:
		return le.AppendUint16(nil, id.DeviceType)
	case 3:
		return le.AppendUint16(nil, id.ProductCode)
	case 4:
		return []byte{33, 11}
	case 5:
		return le.AppendUint16(nil, id.Status)
	case 6:
		return le.AppendUint32(nil, s.serialNum)
	case 7:
		return append([]byte{byte(len(id.ProductName))}, id.ProductName...)
	}
	return nil
}

// classControllerName is the Logix class whose instance 1 answers
// Get_Attributes_All with the controller name.
const classControllerName = 0x64

func (s *Server) attrService(svc byte, path, d []byte) []byte {
	class, inst, attr, ok := parseLogical(path)
	if !ok {
		return enip.MarshalResponse(svc, enip.StatusPathSegment, nil, nil)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if class == enip.ClassIdentity {
		if inst != 1 {
			return enip.MarshalResponse(svc, enip.StatusObjectMissing, nil, nil)
		}
		switch svc {
		case enip.SvcGetAttributesAll:
			var out []byte
			for a := 1; a <= 7; a++ {
				out = append(out, s.identityAttr(a)...)
			}
			return enip.MarshalResponse(svc, 0, nil, out)
		case enip.SvcGetAttributeSingle:
			if b := s.identityAttr(attr); b != nil {
				return enip.MarshalResponse(svc, 0, nil, b)
			}
			return enip.MarshalResponse(svc, enip.StatusAttrNotSupported, nil, nil)
		case enip.SvcSetAttributeSingle:
			if s.identityAttr(attr) == nil {
				return enip.MarshalResponse(svc, enip.StatusAttrNotSupported, nil, nil)
			}
			return enip.MarshalResponse(svc, enip.StatusNotSettable, nil, nil)
		}
		return enip.MarshalResponse(svc, enip.StatusServiceNotSup, nil, nil)
	}
	if class == classControllerName && inst == 1 && svc == enip.SvcGetAttributesAll {
		name := binary.LittleEndian.AppendUint16(nil, uint16(len(s.ControllerName)))
		return enip.MarshalResponse(svc, 0, nil, append(name, s.ControllerName...))
	}
	objectKnown := false
	for k := range s.attrs {
		if k.Class == class && k.Instance == inst {
			objectKnown = true
		}
	}
	if !objectKnown {
		return enip.MarshalResponse(svc, enip.StatusObjectMissing, nil, nil)
	}
	a := s.attrs[AttrKey{class, inst, attr}]
	switch svc {
	case enip.SvcGetAttributeSingle:
		if a == nil {
			return enip.MarshalResponse(svc, enip.StatusAttrNotSupported, nil, nil)
		}
		return enip.MarshalResponse(svc, 0, nil, a.Data)
	case enip.SvcSetAttributeSingle:
		switch {
		case a == nil:
			return enip.MarshalResponse(svc, enip.StatusAttrNotSupported, nil, nil)
		case !a.Settable:
			return enip.MarshalResponse(svc, enip.StatusNotSettable, nil, nil)
		case len(d) < len(a.Data):
			return enip.MarshalResponse(svc, enip.StatusNotEnoughData, nil, nil)
		case len(d) > len(a.Data):
			return enip.MarshalResponse(svc, enip.StatusTooMuchData, nil, nil)
		case s.writeStatus != 0:
			return enip.MarshalResponse(svc, s.writeStatus, nil, nil)
		}
		a.Data = append([]byte(nil), d...)
		return enip.MarshalResponse(svc, 0, nil, nil)
	}
	return enip.MarshalResponse(svc, enip.StatusServiceNotSup, nil, nil)
}
