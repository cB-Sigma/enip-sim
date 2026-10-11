# Changelog

## v0.1.0 (2026-10-11)

First release, checked with pycomm3 1.2.16 (see README.md, "Interop
tested against").

Changed while preparing it:

- pycomm3's `LogixDriver` could not connect: on open it asks for the
  controller name (Get_Attributes_All on class 0x64 instance 1), which
  the simulator answered with "service not supported". It now answers
  with a CIP STRING from the new field `Server.ControllerName` (default
  "SimController"); attribute 1 of that instance is unchanged
  (`TestControllerNameAnswered`).
- pycomm3 1.2.16 sends a single-tag Write Tag over a connection with the
  request repeated after the value; the simulator refused it as too much
  data. It now writes the elements the request names and ignores what
  follows; a value that is too short is still refused
  (`TestWriteTagIgnoresTrailingBytes`). That real controllers accept this
  is inferred from pycomm3's wide use, not checked on hardware.
