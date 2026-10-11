// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

// Package interop holds tests only: pywizlight (MIT), the WiZ library
// that Home Assistant uses, written independently of this module, drives
// the simulated devices of github.com/cB-Sigma/wiz-sim: discovery,
// the bulb type of every module class (getSystemConfig, getModelConfig,
// getUserConfig), colour, brightness, colour temperature, scenes, speed,
// setState, on and off, refusals, power on the plug, fans, the ratio of a
// dual-head lamp, per-head state of a dual-head lamp, syncPilot and
// firstBeat pushes, and reboot. The setPilot params pywizlight sends for
// a hue and saturation, or an RGB colour, are compared with the wiz
// module's HSToRGBWPywizlight and RGBToRGBWPywizlight.
//
// The simulator runs in the test process; pywizlight runs in a pinned
// Python image, which installs the pinned pywizlight package from PyPI and
// runs testdata/pywizlight_client.py, our own script driving pywizlight's
// classes. No pywizlight code is kept in this repository. The tests run
// only when SIGMA_WIZ_INTEROP=1 and docker is on PATH, and need network
// access for the package and UDP port 38899 free on the host (pywizlight
// sends discovery to that port only):
//
//	cd interop && SIGMA_WIZ_INTEROP=1 GOWORK=off go test -v -timeout 10m ./...
//
// The container reaches the simulator at host.docker.internal. On macOS
// (Docker Desktop) the simulator listens on 127.0.0.1; elsewhere on all
// interfaces for the duration of the test. pywizlight listens for pushes
// on port 38900 in the container, which is published on a free port of
// the host's 127.0.0.1; the simulator pushes there (sim.SetPushPort), and
// the script registers 127.0.0.1 as pywizlight's phoneIp, standing in for
// the container's own address, which the host cannot reach.
package interop
