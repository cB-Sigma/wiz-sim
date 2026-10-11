# wiz-sim

Simulated WiZ lights and plugs on the local UDP API, for testing WiZ clients without devices; not affiliated with the owner of the WiZ brand.

[![Go Reference](https://pkg.go.dev/badge/github.com/cB-Sigma/wiz-sim.svg)](https://pkg.go.dev/github.com/cB-Sigma/wiz-sim) [![test](https://github.com/cB-Sigma/wiz-sim/actions/workflows/test.yml/badge.svg)](https://github.com/cB-Sigma/wiz-sim/actions/workflows/test.yml) [![Licence: Apache-2.0](https://img.shields.io/badge/licence-Apache--2.0-blue.svg)](LICENSE) [![Go](https://img.shields.io/badge/go-1.22-00ADD8.svg)](go.mod)

## What is simulated

| Feature | Status |
|---|---|
| One UDP socket per device, answering getPilot, setPilot, getSystemConfig, getPower and registration | yes |
| A socket standing in for the broadcast address, so discovery reaches every device | yes |
| syncPilot pushes to registered addresses on state change, sent from a port of the device's own; firstBeat on demand; acknowledgements counted | yes |
| Module classes | colour, tunable white, dimmable white, plug (any module name can be configured) |
| Faults | a device can be taken offline (ignores every datagram) |
| Counters | requests per method, setPilot parameters per device, registrations, acknowledgements |
| Wi-Fi setup, firmware update, the cloud API | no |

Sockets are on 127.0.0.1 unless another IPv4 address is given. Real
devices each listen on port 38899 of their own address; here each
device has its own port on one address.

## Install

```
go get github.com/cB-Sigma/wiz-sim@latest
```

Requires Go 1.22 or later.

## Command

```
go run github.com/cB-Sigma/wiz-sim/cmd/wizsim@latest -host 127.0.0.1 -port 38899 -toggle 10s
```

| Flag | Default | Meaning |
|---|---|---|
| `-host` | 127.0.0.1 | IPv4 address the devices listen on |
| `-port` | 0 | UDP port of the first device; the others follow (0: free ports) |
| `-toggle` | 0 | switch the colour bulb on and off at this interval, so registered clients receive pushes (0: never) |

Output of the invocation above:

```
wizsim: a8bb50e4c101 ESP01_SHRGB1C_31 on udp 127.0.0.1:38899
wizsim: a8bb50e4c102 ESP56_SHTW3_01   on udp 127.0.0.1:38900
wizsim: a8bb50e4c103 ESP05_SHDW9_01   on udp 127.0.0.1:38901
wizsim: a8bb50e4c104 ESP10_SOCKET_06  on udp 127.0.0.1:38902
wizsim: discovery broadcasts to udp 127.0.0.1:60593
```

The discovery port is chosen freely on each run. Asking the tunable-white
bulb for its state:

```
$ echo '{"method":"getPilot","params":{}}' | nc -u -w1 127.0.0.1 38900
{"env":"pro","method":"getPilot","result":{"dimming":60,"mac":"a8bb50e4c102","rssi":-58,"sceneId":0,"src":"","state":true,"temp":2700}}
```

The command stops on Ctrl-C or SIGTERM.

## Quick start: in Go tests

Discover the simulated devices with
[wiz](https://github.com/cB-Sigma/wiz), read one and switch it to a scene
(`Example_discoverAndSetScene` in sim/wiz_integration_test.go, run by
`go test`):

```go
s := sim.NewDemo() // stands in for the LAN; use "255.255.255.255:38899" there
defer s.Close()
ctx := context.Background()

found, _ := wiz.Discover(ctx, s.BroadcastAddr(), "02aabbccddee", time.Second)
addr := found["a8bb50e4c102"]

raw, _ := wiz.Request(ctx, addr, wiz.MethodGetSystemConfig, nil, 3*time.Second)
cfg, _ := wiz.ParseSystemConfig(raw)
class, _ := wiz.ClassOf(cfg.ModuleName)
fmt.Println(len(found), "devices;", cfg.ModuleName, "is", class)

id, _ := wiz.SceneID("warm white")
if wiz.HasScene(class, id) {
	_, err := wiz.Request(ctx, addr, wiz.MethodSetPilot, map[string]any{"sceneId": id}, 3*time.Second)
	fmt.Println("scene set:", err == nil)
}
raw, _ = wiz.Request(ctx, addr, wiz.MethodGetPilot, nil, 3*time.Second)
p, _ := wiz.ParsePilot(raw)
fmt.Println("now:", wiz.SceneName(int(*p.SceneID)))
// Output:
// 4 devices; ESP56_SHTW3_01 is tw
// scene set: true
// now: Warm white
```

A test can change a device's state (which pushes to registered
addresses) and check what the client sent:

```go
s.SetState("a8bb50e4c101", func(st *sim.State) { st.On = false })
s.SetOffline("a8bb50e4c103", true)
n := s.Registrations("a8bb50e4c101")
```

`sim.New` or `sim.NewHost` start an empty simulator; `Add` and `AddAt`
add devices with any MAC, module name, firmware and state.

## Documentation

[pkg.go.dev/github.com/cB-Sigma/wiz-sim](https://pkg.go.dev/github.com/cB-Sigma/wiz-sim)

| Package | What it does |
|---|---|
| `sim` | Simulated lights and plugs, the broadcast socket, pushes, fault switches and counters |
| `cmd/wizsim` | Serves four demo devices: colour, tunable white, dimmable white, plug |

## Testing

```
go test -race ./...
```

The tests cover the simulator itself, the command, and the wiz client's
discovery, registration, pushes and requests against the simulator.

### Interop tested against

The simulator is written from the same public descriptions as the wiz
client; agreement between them is not independent evidence. It has not
been compared with a real WiZ device.

## Demo values

Every MAC address, module name, firmware version, power reading and
address in this module is a made-up demo value. There are no keys or
credentials.

## Related repositories

- [wiz](https://github.com/cB-Sigma/wiz): the Go client for the WiZ
  local UDP API that this module tests.

## Status

v0.x; the API may change before v1.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## Security

See [SECURITY.md](SECURITY.md).

## Licence

Apache-2.0 (LICENSE). Copyright Abhinav Gautam. NOTICE lists the
projects whose public descriptions supplied the protocol facts. WiZ is a
trademark of its owner; the name is used only to say which devices this
module simulates.
