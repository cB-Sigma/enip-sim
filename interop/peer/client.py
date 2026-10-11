# Copyright 2026 Abhinav Gautam
# SPDX-License-Identifier: Apache-2.0
#
# pycomm3's drivers against the simulated Logix controller, for interop
# tests.
#
#   python client.py logix <path> large|small <cpu slot>
#   python client.py discover <address>
#
# "logix" identifies the target with CIPDriver.list_identity, opens a
# LogixDriver (Large Forward Open with a 4000-byte connection, or
# "small": the standard Forward Open with 500 bytes, so big values move
# in fragments), reads and writes tags and sends generic messages; it
# prints "PEER <step> <json>" after each step and "PEER done" at the end.
# "discover" sends pycomm3's ListIdentity broadcast to <address>.
#
# The simulator has no tag browsing (symbol and template objects), so the
# driver opens with init_tags=False and is given the tag definitions
# pycomm3 would otherwise have uploaded, in its own format; reads and
# writes then use symbolic paths (use_instance_ids off).

import json
import sys
import traceback

from pycomm3 import CIPDriver, LogixDriver, Services, ClassCode
from pycomm3.cip import DataTypes, Array, DINT, SINT, UINT, SHORT_STRING
from pycomm3.custom_types import FixedSizeString


def say(step, obj=None):
    print("PEER " + step + ("" if obj is None else " " + json.dumps(obj, sort_keys=True, default=str)), flush=True)


def tag(t):
    """A Tag result as a dict."""
    return {"tag": t.tag, "value": t.value, "type": t.type, "error": t.error}


def atomic(name, typ, n=0):
    tc = DataTypes.get(typ)
    return {
        "tag_name": name,
        "dim": 1 if n else 0,
        "alias": False,
        "instance_id": 0,
        "symbol_address": 0,
        "symbol_object_address": 0,
        "software_control": 0,
        "external_access": "Read/Write",
        "dimensions": [n, 0, 0],
        "data_type": typ,
        "data_type_name": typ,
        "tag_type": "atomic",
        "type_class": Array(length_=n, element_type_=tc) if n else tc,
    }


def string(name):
    # The built-in Logix STRING as pycomm3 describes it after reading its
    # template: LEN DINT, DATA SINT[82], 88 bytes, handle 0x0FCE.
    dt = {
        "name": "STRING",
        "internal_tags": {
            "LEN": {"offset": 0, "tag_type": "atomic", "data_type": "DINT", "data_type_name": "DINT", "type_class": DINT},
            "DATA": {"offset": 4, "tag_type": "atomic", "data_type": "SINT", "data_type_name": "SINT", "array": 82,
                     "type_class": Array(length_=82, element_type_=SINT)},
        },
        "attributes": ["LEN", "DATA"],
        "template": {"structure_handle": 0x0FCE, "structure_size": 88, "member_count": 2, "object_definition_size": 0},
        "string": 82,
        "type_class": FixedSizeString(84),
    }
    return {
        "tag_name": name,
        "dim": 0,
        "alias": False,
        "instance_id": 0,
        "symbol_address": 0,
        "symbol_object_address": 0,
        "software_control": 0,
        "external_access": "Read/Write",
        "dimensions": [0, 0, 0],
        "data_type": dt,
        "data_type_name": "STRING",
        "tag_type": "struct",
        "template_instance_id": 0x0FCE,
        "type_class": dt["type_class"],
    }


TAGS = {
    "Flow": atomic("Flow", "REAL"),
    "Program:Main.Count": atomic("Program:Main.Count", "DINT"),
    "Running": atomic("Running", "BOOL"),
    "Levels": atomic("Levels", "INT", 10),
    "Name": string("Name"),
    "Big": atomic("Big", "DINT", 300),
    "Total": atomic("Total", "LREAL"),
}


def logix(path, size, slot):
    say("list_identity", CIPDriver.list_identity(path))

    plc = LogixDriver(path, init_tags=False, init_program_tags=False)
    if size == "small":
        plc._cfg["extended forward open"] = False
        plc._cfg["connection_size"] = 500
    plc.socket_timeout = 5
    plc.open()
    say("open", {"info": plc.info, "name": plc.name, "connected": plc.connected,
                 "extended": plc._cfg["extended forward open"], "connection_size": plc.connection_size})
    plc._cfg["use_instance_ids"] = False
    plc._tags = dict(TAGS)

    say("read_atomic", [tag(t) for t in (plc.read("Flow"), plc.read("Program:Main.Count"), plc.read("Running"), plc.read("Total"))])
    say("write_atomic", [tag(t) for t in plc.write(("Flow", 3.25), ("Program:Main.Count", -7), ("Total", 2.5))])
    say("read_back", [tag(t) for t in plc.read("Flow", "Program:Main.Count", "Total")])
    say("write_readonly", tag(plc.write("Running", False)))

    say("read_array", tag(plc.read("Levels{10}")))
    say("write_array", tag(plc.write("Levels{10}", [i * 3 for i in range(10)])))
    say("read_element", tag(plc.read("Levels[4]")))

    say("read_string", tag(plc.read("Name")))
    say("write_string", tag(plc.write("Name", "Hello pycomm3")))
    say("read_string2", tag(plc.read("Name")))

    big = plc.read("Big{300}")
    say("read_big", {"error": big.error, "type": big.type, "n": len(big.value or []),
                     "ok": big.value == list(range(300))})
    want = [i * 2 + 1 for i in range(300)]
    say("write_big", tag(plc.write("Big{300}", want)))
    big = plc.read("Big{300}")
    say("read_big2", {"error": big.error, "n": len(big.value or []), "ok": big.value == want})

    # A Multiple Service Packet of several reads.
    say("read_multi", [tag(t) for t in plc.read("Flow", "Program:Main.Count", "Levels{10}", "Total", "Name")])

    # Generic messaging to the identity object, unconnected and connected.
    say("generic_unconnected", tag(plc.generic_message(
        service=Services.get_attribute_single, class_code=ClassCode.identity_object, instance=1, attribute=7,
        data_type=SHORT_STRING, connected=False, unconnected_send=True, name="product_name")))
    say("generic_connected", tag(plc.generic_message(
        service=Services.get_attribute_single, class_code=ClassCode.identity_object, instance=1, attribute=1,
        data_type=UINT, connected=True, name="vendor")))
    # The identity of the module in the CPU's slot, through Unconnected Send.
    say("module_info", plc.get_module_info(int(slot)))

    plc.close()
    say("done")


def discover(address):
    say("discover", CIPDriver.discover(address))


mode = sys.argv[1]
try:
    if mode == "logix":
        logix(*sys.argv[2:5])
    elif mode == "discover":
        discover(sys.argv[2])
except Exception as e:
    traceback.print_exc()
    say("exception", {"error": repr(e), "cause": repr(e.__cause__)})
