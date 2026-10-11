// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

// Package sim is an in-process EtherNet/IP adapter that behaves like a
// Logix controller for tests. Since v0.2.0 the adapter itself is the enip
// module's server package (the target role as a library); this package
// keeps its names, so code written against v0.1.0 builds unchanged, and
// gives it a demo tag table.
package sim

import (
	"encoding/binary"
	"math"

	"github.com/cB-Sigma/enip"
	"github.com/cB-Sigma/enip/server"
)

// Server is the simulated adapter (server.Server).
type Server = server.Server

// Tag, Attr, AttrKey and Stats are the server package's types.
type (
	Tag     = server.Tag
	Attr    = server.Attr
	AttrKey = server.AttrKey
	Stats   = server.Stats
)

// RecipeDef is the demo structure of the Recipe tag.
var RecipeDef = server.StructDef{Name: "Recipe", Handle: 0x1234, Size: 12, Fields: []server.Field{
	{Name: "Count", Type: enip.Type{Code: enip.TypeDINT}, Offset: 0},
	{Name: "Speed", Type: enip.Type{Code: enip.TypeREAL}, Offset: 4},
	{Name: "Temp", Type: enip.Type{Code: enip.TypeINT}, Offset: 8},
}}

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
	s := server.New()
	s.ControllerName = "SimController"
	s.SetIdentity(enip.Identity{Protocol: 1, Vendor: 1, DeviceType: 0x0E, ProductCode: 166, ProductName: "1756-L83E/B Sim"}, 0x00C0FFEE)
	s.DefineStruct(RecipeDef)
	le := binary.LittleEndian
	s.SetTag("Flow", enip.Type{Code: enip.TypeREAL}, 1, le.AppendUint32(nil, math.Float32bits(12.5)))
	s.SetTag("Program:Main.Count", enip.Type{Code: enip.TypeDINT}, 1, le.AppendUint32(nil, 42))
	s.SetTag("Running", enip.Type{Code: enip.TypeBOOL}, 1, []byte{1})
	s.SetReadOnly("Running", true)
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
	s.SetAttr(AttrKey{Class: 0x64, Instance: 1, Attribute: 1}, Attr{Data: le.AppendUint32(nil, 5), Settable: true})
	return s
}
