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

### v0.2.0 (2026-10-11)

v0.2.0 adds fans, dual-head ratio, setState, the configuration and
maintenance methods, power in the pilot and firmware-update pushes. Their
facts were read, for facts only, from the sources below at the commits
named, the same reading as the wiz client's v0.2.0 (see its
PROVENANCE.md for which fact came from which source). wiz-local-control
is the vendor's own library (MIT, Copyright (c) 2018 WiZLighting); it was
read for facts the same way and no code was taken from it. A line-by-line
comparison with these sources was not needed, at the user's direction:
nothing was translated from them.

Where the sources are silent, the simulator's behaviour is its own
choice and is documented as such in the README and on the methods: what
reset keeps, the updateStatus values sent during a firmware update, the
content of getWifiConfig, registrations surviving a restart, the refusal
of unknown setUserConfig keys, and every configuration value it reports.

## Sources

| Source | Commit read | Licence |
|---|---|---|
| [PY] pywizlight, github.com/sbidy/pywizlight | master c6ce9d97677aab1da3a693973fb3487383f5d235 (latest tag v0.6.6) | MIT, Copyright (c) 2020 Stephan Traub |
| [HA] Home Assistant, github.com/home-assistant/core, homeassistant/components/wiz | dev 8792c68d | Apache-2.0 |
| [OH] openHAB add-ons, github.com/openhab/openhab-addons, bundles/org.openhab.binding.wiz | main a5a74fd0 | EPL-2.0 |
| [WZ] wiz-local-control, gitlab.com/wizlighting/wiz-local-control (the vendor's own library) | master b7bfe09f (2025-09-03) | MIT, Copyright (c) 2018 WiZLighting |

| Area (files) | Followed | Read for facts only (no code taken) |
|---|---|---|
| Simulated devices, discovery socket, pushes (sim/) | none published | PY: method and field names, ports, registration and push messages, module names, the class order, fan keys and ranges, ratio, setState, getModelConfig and getUserConfig keys, getPower and pc, reboot and reset. WZ: setUserConfig keys, updateOta params and progress pushes, the acknowledgement, fwOtaStatus, firstBeat as an announcement after booting, the 1 to 100 dimming range. HA, OH: fan modes and the example module ESP03_FANDIMS_31 |
| Command (cmd/wizsim) | none | none; our own code |
| Integration tests (sim/wiz_integration_test.go, sim/methods_test.go) | none | none; they drive github.com/cB-Sigma/wiz |

## Test data

All MAC addresses, module names, firmware versions, power readings and
addresses in the simulator, the tests and cmd/wizsim are made-up demo
values. The module names are real model-name forms, chosen to exercise
each class; the MAC prefix is that of WiZ devices, the suffixes are
sequential.
