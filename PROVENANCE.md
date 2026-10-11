# Provenance

## How the code was written

Written for SigmaM2M, a oneM2M platform, in Go, by the author of the enip
module and from the same documents (the ODVA CIP Networks Library and
Rockwell Automation's Logix 5000 data access manual, as facts). No code
was ported or copied from another implementation. The simulated
controller came with SigmaM2M's baseline (2026-09-27), next to its
EtherNet/IP adapter. The simulator, its command and the tests that drive
it moved out of the enip module into this one on 2026-10-11 (with
`git mv`), so the library does not depend on its simulator, and the
pycomm3 interop tests (interop/) were added. They found the defects fixed
in v0.1.0 (CHANGELOG.md).

## Sources

| Area (files) | Followed |
|---|---|
| Simulator (sim/sim.go) | as for the enip client; it reuses that module's codecs |
| Command (cmd/enipsim) | none (flags are our own) |
| Interop peer (interop/peer/client.py) | pycomm3 1.2.16's public API, read to write our client program; its behaviour on the wire was observed as a peer |

## Tests

| Test | Source |
|---|---|
| trailingwrite_test.go | the requests pycomm3 1.2.16 sent in the interop tests (a Write Tag with trailing octets; Get_Attributes_All for the controller name), rebuilt by hand |
| client_test.go, idleconn_test.go, partialwrite_test.go, example_test.go, cmd/enipsim/main_test.go | the enip client against this simulator, written from the same documents; not a real device |
| interop/ | pycomm3 1.2.16 |

All tag names, values, serial numbers and addresses are made-up demo
values.
