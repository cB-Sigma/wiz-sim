# Changelog

All notable changes to this module. Versions follow semantic versioning;
before v1 the API may change in minor versions.

## v0.3.0 (2026-10-11)

Needs github.com/cB-Sigma/wiz v0.3.0 for its tests.

### Added

- Dual-head lamps with a state per head: `Device.PerHead` and
  `Device.Head2`, with pywizlight 0.6.6's devices numbering in getPilot,
  setPilot, setState and syncPilot; `Sim.HeadState`, `Sim.SetHeadState`.
- pulse (delta and duration checked, counted by `Sim.Pulses`).
- `Sim.SetPushPort`: push to the registration's phoneIp at a port, as
  real devices push to phoneIp, port 38900.
- `NewHostAt` (the broadcast socket on a chosen port) and
  `Sim.SetBroadcastAnswers` (answer the broadcast socket's datagrams from
  it, for clients behind NAT).
- wizsim flags `-bcast-port` and `-push-port`.
- The interop module: pywizlight 0.6.6's client against the simulator,
  in Docker (SIGMA_WIZ_INTEROP=1).

### Fixed

- Pushes could only go to the registration's source address; pywizlight
  listens at phoneIp, port 38900, and so missed them. `SetPushPort`
  sends them there (opt-in; the default is unchanged).

## v0.2.0 (2026-10-11)

Needs github.com/cB-Sigma/wiz v0.2.0 for its tests and the command.

### Added

- Fan modules (FANDIM, DDTW): fanState, fanMode, fanSpeed and fanRevrs in
  setPilot and the pilot, checked and refused with -32602 when out of
  range or sent to a module without a fan; `State` fields `FanState`,
  `FanMode`, `FanSpeed`, `FanRevrs`; `Device.FanSpeeds`.
- Dual-head modules (DH): ratio 0 to 100; `State.Ratio`.
- setState: setPilot's params without switching the light on.
- getModelConfig (with `Device.NoModelConfig` for firmware before 1.22),
  getUserConfig and setUserConfig (documented keys, stored),
  getWifiConfig (an opaque string), setSystemConfig and setModelConfig
  (recorded, not applied); fwOtaStatus in getSystemConfig.
- reboot (silent for `SetRebootTime`, 2 s by default, then a firstBeat),
  reset (back to the initial state, off the network until
  `SetOffline(mac, false)`), updateOta (progress pushes, a restart, the
  new firmware; `Device.LatestFirmware` for "default").
- `pc` (milliwatts) in plugs' pilots.
- `Device.MinDimming`, the lowest dimming accepted (10 by default).
- `Sim.Requests` (params of every request per device and method) and
  `Sim.Registered`.
- `Demo`, `ExtendedDemo` and `NewExtendedDemo`: the four demo devices
  plus a ceiling fan with a dimmable light, one with a tunable-white light
  and a dual-head lamp.
- wizsim serves the extended demo (seven devices) and takes `-reboot`.
- CHANGELOG.md.

### Changed

- setPilot refuses r, g, b, c and w outside 0 to 255, c and w on
  non-colour modules, speed outside 10 to 200, a state that is not a
  boolean and any value that is not a whole number; the methods above are
  answered instead of "method not found".
- `NewDemo` is unchanged (four devices); the colour bulb now offers
  firmware 1.32.0 to updateOta.

## v0.1.0

Inferred from the repository history; the release date is not recorded
here. First release (2026-10-11): the simulator, its command and the
integration tests of the wiz client, moved out of the wiz module.
