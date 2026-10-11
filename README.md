# enip-sim

A simulated Logix controller over EtherNet/IP in Go, for testing EtherNet/IP and CIP clients without a PLC.

[![Go Reference](https://pkg.go.dev/badge/github.com/cB-Sigma/enip-sim.svg)](https://pkg.go.dev/github.com/cB-Sigma/enip-sim) [![test](https://github.com/cB-Sigma/enip-sim/actions/workflows/test.yml/badge.svg)](https://github.com/cB-Sigma/enip-sim/actions/workflows/test.yml) [![Licence: Apache-2.0](https://img.shields.io/badge/licence-Apache--2.0-blue.svg)](LICENSE) [![Go](https://img.shields.io/badge/go-1.22-00ADD8.svg)](go.mod)

The simulator keeps a tag table and the identity object, opens sessions,
routes Unconnected Send to a CPU slot, accepts Forward Open and Large
Forward Open (class 3), answers Multiple Service Packets, fragmented
reads and writes and the attribute services, and answers ListIdentity
on TCP and UDP. Switches make it refuse Forward Open, go silent or
answer writes with a chosen status, and counters show what a client did.
It is built on the codecs of [enip](https://github.com/cB-Sigma/enip).
Written for SigmaM2M, a oneM2M platform; it has no dependency on it.

## What is simulated

| Package | What it does |
|---|---|
| `sim` | `New`, `Start(addr)`, `Stop`, `Addr`; tags (`SetTag`, `TagData`, read-only tags), attributes (`AttrData`, settable or not), the controller name (`ControllerName`, read by pycomm3 on open), a CPU slot that requests must route to (`Slot`, `RequireRoute`); faults (`RefuseForwardOpen`, `Silent`, `AnswerWritesWith`); counters (`Stats`: fragmented reads and writes, Multiple Service Packets, Forward Opens and Closes, ...) |
| `cmd/enipsim` | Serves the simulator with its demo tags |

Not simulated: tag browsing (symbol and template objects), implicit I/O,
PCCC, CIP Security.

## Install

```
go get github.com/cB-Sigma/enip-sim@latest
```

Requires Go 1.22 or later. The command:

```
go install github.com/cB-Sigma/enip-sim/cmd/enipsim@latest
```

## Command line

```
enipsim -addr 127.0.0.1:44818 -slot 2
```

| Flag | Default | Meaning |
|---|---|---|
| `-addr` | `127.0.0.1:44818` | TCP and UDP address to listen on |
| `-slot` | `-1` | backplane slot of the CPU; requests must then carry the route (for example `1,2`); -1 for none |

The demo tags: `Flow` (REAL), `Program:Main.Count` (DINT), `Running`,
`Levels`, `Name` (STRING), `Recipe` (a structure) and `Big` (DINT[300],
for fragmentation).

## In Go tests

From `Example`:

```go
plc := sim.New()
plc.Slot, plc.RequireRoute = 2, true
if err := plc.Start("127.0.0.1:0"); err != nil {
	panic(err)
}
defer plc.Stop()
route, _ := enip.Route("1,2")
c := &enip.Client{Addr: plc.Addr(), Route: route, Timeout: time.Second, Connected: true}
ty, data, err := c.ReadTag(ctx, "Flow", 1)
```

## Demo values

Every tag name, value, serial number, address and product name in this
module is a made-up demo value.

## Documentation

Reference: https://pkg.go.dev/github.com/cB-Sigma/enip-sim

## Testing

```
go vet ./...
go test -race ./...
```

The tests at the module root drive the enip client against the
simulator: fragmented and batched reads and writes, connected and
unconnected, routed and direct; a write after the target dropped an idle
connection; writes answered with a partial-transfer status. Tests carry
`// Covers: SIG-...` comments: requirement IDs of the SigmaM2M project's
compliance report, with no meaning outside it. The simulator and the
client are written by the same author from the same documents, so these
tests show that the two agree. The independent check is `interop/`, a
nested test-only module that runs pycomm3 1.2.16 (MIT, installed from
PyPI in a container) against the simulator. It is skipped unless
`SIGMA_ENIP_SIM_INTEROP=1` is set and Docker is running:

```
cd interop && SIGMA_ENIP_SIM_INTEROP=1 go test -race -v ./...
```

### Interop tested against

Run on 2026-10-11 on macOS with Docker Desktop; every item passed.
pycomm3 1.2.16 (`LogixDriver`, `CIPDriver`) against this simulator, over
Large Forward Open (4000 octets) and standard Forward Open (500), each
without a route and with the CPU in slot 2 (path `/1/2`):

| Area | What was checked |
|---|---|
| Identity | `CIPDriver.list_identity`; `open()` with the controller's info and name; `get_module_info`; `discover` over UDP (skipped when port 44818 is taken) |
| Tags | atomic reads and writes, a read-only tag refused; an INT[10] array and one element; STRING read and write |
| Fragmentation | `Big` (DINT[300]) read and written: in fragments over the 500-octet connection, in one piece over 4000 (checked with the simulator's counters) |
| Multiple Service Packet | multi-tag reads arriving as one packet |
| Generic messages | Get_Attribute_Single on the identity object, unconnected (through Unconnected Send) and connected |

pycomm3 normally reads the controller's tag list on open; the simulator
has no symbol or template objects, so the test opens with tag
initialisation off and gives pycomm3 the tag definitions itself.
Instance-ID addressing and structure reads through pycomm3 are therefore
not covered.

Defects of the simulator found by these tests and fixed in v0.1.0 are in
CHANGELOG.md.

## Related repositories

- [enip](https://github.com/cB-Sigma/enip): the EtherNet/IP client.

## Status

v0.x. The API may change before v1.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## Security

See [SECURITY.md](SECURITY.md).

## Licence

Apache-2.0 (see [LICENSE](LICENSE)). Copyright 2026 Abhinav Gautam.
[NOTICE](NOTICE) names the trademarks used.
