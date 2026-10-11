# Provenance

## How the code was written

Written in Go for SigmaM2M, a oneM2M platform, to test its WiZ client
without devices. No code was ported or copied from another
implementation or simulator, and no source file of another project was
translated.

The vendor does not publish the WiZ local UDP API, so there is no
standard to follow. The simulator answers with the same interface facts
the client uses (ports, the JSON message shape, the method and pilot
field names, the registration parameters, syncPilot and firstBeat
pushes, module names and scene numbers), read from public descriptions
of the API, chiefly the pywizlight project and its documentation. That
devices answer requests on their API port but push from a port of their
own is modelled after the behaviour those descriptions report.

sim/sim.go came from SigmaM2M's first version (2026-09-27) and moved to
this module on 2026-10-11, together with the command and the
integration tests that drive the client against the simulator. The
command and the tests marked "Copyright 2026 Abhinav Gautam" were
written for SigmaM2M and this module.

## Sources

| Area (files) | Followed | Read for facts only (no code taken) |
|---|---|---|
| Simulated devices, discovery socket, pushes (sim/) | none published | pywizlight (github.com/sbidy/pywizlight, MIT): method and field names, ports, registration and push messages, module names |
| Command (cmd/wizsim) | none | none; our own code |
| Integration tests (sim/wiz_integration_test.go) | none | none; they drive github.com/cB-Sigma/wiz |

## Test data

All MAC addresses, module names, firmware versions, power readings and
addresses in the simulator, the tests and cmd/wizsim are made-up demo
values. The module names are real model-name forms, chosen to exercise
each class; the MAC prefix is that of WiZ devices, the suffixes are
sequential.
