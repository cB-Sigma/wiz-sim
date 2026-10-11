// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package sim_test

// The methods added in v0.2.0: fans, dual-head ratio, setState, power in
// the pilot, configuration, reboot, reset and firmware update, driven
// through github.com/cB-Sigma/wiz where it has a call for them.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/cB-Sigma/wiz"

	"github.com/cB-Sigma/wiz-sim/sim"
)

const (
	fanDimMAC = "a8bb50e4c1d1"
	fanTWMAC  = "a8bb50e4c1d2"
)

// Put the simulated ceiling fan in breeze mode; its light stays off.
func Example_fan() {
	s := sim.NewExtendedDemo()
	defer s.Close()
	ctx := context.Background()
	addr := s.Addr("a8bb50e4c105") // the ceiling fan with a dimmable light

	model, _ := wiz.GetModelConfig(ctx, addr, 3*time.Second)
	speeds := wiz.FanSpeedMax(model, wiz.UserConfig{})
	on := true
	params, _ := wiz.FanParams{On: &on, Mode: wiz.FanModeBreeze}.Params(speeds)
	_, err := wiz.Request(ctx, addr, wiz.MethodSetPilot, params, 3*time.Second)
	fmt.Println("speeds:", speeds, "set:", err == nil)

	st := s.State("a8bb50e4c105")
	fmt.Println("fan mode:", st.FanMode, "light on:", st.On)
	// Output:
	// speeds: 6 set: true
	// fan mode: 2 light on: false
}

func refused(t *testing.T, err error, code int) bool {
	t.Helper()
	var de *wiz.DeviceError
	return errors.As(err, &de) && de.Code == code
}

func TestFans(t *testing.T) {
	s := sim.New()
	defer s.Close()
	dim := s.Add(sim.Device{MAC: fanDimMAC, Module: "ESP03_FANDIMS_31", Firmware: "1.31.0", State: sim.State{Dimming: 100, FanMode: 1, FanSpeed: 1}})
	tw := s.Add(sim.Device{MAC: fanTWMAC, Module: "ESP20_DDTW1_01", Firmware: "1.30.0", FanSpeeds: 4, State: sim.State{On: true, Dimming: 50, Temp: 3000, FanMode: 1, FanSpeed: 1}})
	rgb := s.Add(sim.Device{MAC: "a8bb50e4c101", Module: "ESP01_SHRGB1C_31", Firmware: "1.28.0", State: sim.State{On: true, Dimming: 50}})
	ctx := context.Background()

	raw, _ := wiz.Request(ctx, dim, wiz.MethodGetSystemConfig, nil, 3*time.Second)
	cfg, _ := wiz.ParseSystemConfig(raw)
	if c, ok := wiz.ClassOf(cfg.ModuleName); !ok || c != wiz.ClassFanDim {
		t.Errorf("class %s %v", c, ok)
	}
	for addr, want := range map[string]int{dim: 6, tw: 4} {
		m, err := wiz.GetModelConfig(ctx, addr, 3*time.Second)
		if err != nil || wiz.FanSpeedMax(m, wiz.UserConfig{}) != want {
			t.Errorf("%s: fan speeds %+v %v, want %d", addr, m.FanSpeed, err, want)
		}
	}
	if m, _ := wiz.GetModelConfig(ctx, rgb, 3*time.Second); m.FanSpeed == nil || *m.FanSpeed != 0 {
		t.Errorf("a bulb reports fan speeds %v", m.FanSpeed)
	}

	// The fan runs while the light stays off.
	on, rev := true, true
	p, _ := wiz.FanParams{On: &on, Mode: wiz.FanModeBreeze, Speed: 6, Reverse: &rev}.Params(6)
	if _, err := wiz.Request(ctx, dim, wiz.MethodSetPilot, p, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	st := s.State(fanDimMAC)
	if st.On || st.FanState != 1 || st.FanMode != 2 || st.FanSpeed != 6 || st.FanRevrs != 1 {
		t.Errorf("state %+v", st)
	}
	raw, _ = wiz.Request(ctx, dim, wiz.MethodGetPilot, nil, 3*time.Second)
	pl, _ := wiz.ParsePilot(raw)
	if *pl.State || *pl.FanState != 1 || *pl.FanMode != 2 || *pl.FanSpeed != 6 || *pl.FanRevrs != 1 {
		t.Errorf("pilot %s", raw)
	}
	// Switching the light leaves the fan alone.
	if _, err := wiz.Request(ctx, dim, wiz.MethodSetPilot, map[string]any{"state": true, "dimming": 40}, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if st := s.State(fanDimMAC); !st.On || st.FanState != 1 {
		t.Errorf("state %+v", st)
	}

	for addr, params := range map[string][]map[string]any{
		dim: {{"fanSpeed": 7}, {"fanSpeed": 0}, {"fanMode": 3}, {"fanRevrs": 2}, {"fanState": 2}, {"fanState": true}, {"temp": 3000}},
		tw:  {{"fanSpeed": 5}},
		rgb: {{"fanState": 1}, {"fanSpeed": 2}},
	} {
		for _, p := range params {
			if _, err := wiz.Request(ctx, addr, wiz.MethodSetPilot, p, 3*time.Second); !refused(t, err, wiz.CodeInvalidParams) {
				t.Errorf("%s %v = %v", addr, p, err)
			}
		}
	}
	if _, err := wiz.Request(ctx, tw, wiz.MethodSetPilot, map[string]any{"temp": 4000, "fanSpeed": 4}, 3*time.Second); err != nil {
		t.Errorf("tunable-white fan: %v", err)
	}
	if st := s.State(fanTWMAC); st.Temp != 4000 || st.FanSpeed != 4 {
		t.Errorf("state %+v", st)
	}
}

func TestSetStateDoesNotSwitchOn(t *testing.T) {
	s := sim.New()
	defer s.Close()
	const mac = "a8bb50e4c1aa"
	addr := s.Add(sim.Device{MAC: mac, Module: "ESP05_SHDW9_01", Firmware: "1.22.0", State: sim.State{Dimming: 100}})
	ctx := context.Background()
	if _, err := wiz.Request(ctx, addr, wiz.MethodSetState, map[string]any{"dimming": 50}, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if st := s.State(mac); st.On || st.Dimming != 50 {
		t.Errorf("after setState: %+v", st)
	}
	if _, err := wiz.Request(ctx, addr, wiz.MethodSetState, map[string]any{"dimming": 5}, 3*time.Second); !refused(t, err, wiz.CodeInvalidParams) {
		t.Errorf("setState dimming 5 = %v", err)
	}
	if _, err := wiz.Request(ctx, addr, wiz.MethodSetPilot, map[string]any{"dimming": 60}, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if st := s.State(mac); !st.On || st.Dimming != 60 {
		t.Errorf("after setPilot: %+v", st)
	}
	// A lower dimming floor can be configured.
	low := s.Add(sim.Device{MAC: "a8bb50e4c1ab", Module: "ESP05_SHDW9_01", MinDimming: 1, State: sim.State{Dimming: 100}})
	if _, err := wiz.Request(ctx, low, wiz.MethodSetPilot, map[string]any{"dimming": 1}, 3*time.Second); err != nil {
		t.Errorf("dimming 1 with MinDimming 1: %v", err)
	}
}

func TestDualHeadAndPower(t *testing.T) {
	s := sim.New()
	defer s.Close()
	dh := s.Add(sim.Device{MAC: "a8bb50e4c1e1", Module: "ESP20_DHRGB_01", Firmware: "1.21.40", State: sim.State{On: true, Dimming: 80, Ratio: 50, Temp: 2700}})
	plug := s.Add(sim.Device{MAC: "a8bb50e4c104", Module: "ESP10_SOCKET_06", Firmware: "1.26.1", State: sim.State{On: true}, PowerMW: 61500})
	ctx := context.Background()
	p, _ := wiz.RatioParams(30)
	if _, err := wiz.Request(ctx, dh, wiz.MethodSetPilot, p, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	raw, _ := wiz.Request(ctx, dh, wiz.MethodGetPilot, nil, 3*time.Second)
	if pl, _ := wiz.ParsePilot(raw); pl.Ratio == nil || *pl.Ratio != 30 {
		t.Errorf("pilot %s", raw)
	}
	if _, err := wiz.Request(ctx, dh, wiz.MethodSetPilot, map[string]any{"ratio": 101}, 3*time.Second); !refused(t, err, wiz.CodeInvalidParams) {
		t.Errorf("ratio 101 = %v", err)
	}
	if _, err := wiz.Request(ctx, plug, wiz.MethodSetPilot, map[string]any{"ratio": 10}, 3*time.Second); !refused(t, err, wiz.CodeInvalidParams) {
		t.Errorf("ratio on a plug = %v", err)
	}

	raw, _ = wiz.Request(ctx, plug, wiz.MethodGetPilot, nil, 3*time.Second)
	if pl, _ := wiz.ParsePilot(raw); pl.PC == nil || *pl.PC != 61500 {
		t.Errorf("plug pilot %s", raw)
	}
	if mw, err := wiz.GetPower(ctx, plug, 3*time.Second); err != nil || mw != 61500 {
		t.Errorf("power %v %v", mw, err)
	}
	if _, err := wiz.GetPower(ctx, dh, 3*time.Second); !refused(t, err, wiz.CodeMethodNotFound) {
		t.Errorf("power of a bulb = %v", err)
	}
}

func TestConfigMethods(t *testing.T) {
	s := sim.New()
	defer s.Close()
	const mac = "a8bb50e4c101"
	addr := s.Add(sim.Device{MAC: mac, Module: "ESP01_SHRGB1C_31", Firmware: "1.28.0", State: sim.State{On: true, Dimming: 50}})
	old := s.Add(sim.Device{MAC: fanDimMAC, Module: "ESP03_FANDIMS_31", Firmware: "1.20.0", NoModelConfig: true, FanSpeeds: 5})
	ctx := context.Background()

	m, err := wiz.GetModelConfig(ctx, addr, 3*time.Second)
	u, err2 := wiz.GetUserConfig(ctx, addr, 3*time.Second)
	if err != nil || err2 != nil || u.DftDim == nil || *u.DftDim != 100 {
		t.Fatalf("configs %+v %+v %v %v", m, u, err, err2)
	}
	if min, max, ok := wiz.KelvinRange(m, u); !ok || min != 2200 || max != 6500 {
		t.Errorf("kelvin %d..%d", min, max)
	}
	// Firmware before 1.22: no getModelConfig; the fan speeds are in the
	// user config.
	if _, err := wiz.GetModelConfig(ctx, old, 3*time.Second); !refused(t, err, wiz.CodeMethodNotFound) {
		t.Errorf("old getModelConfig = %v", err)
	}
	if u, _ := wiz.GetUserConfig(ctx, old, 3*time.Second); wiz.FanSpeedMax(wiz.ModelConfig{}, u) != 5 {
		t.Errorf("old fan speeds %v", u.FanSpeed)
	}

	dim := 70
	if err := wiz.SetUserConfig(ctx, addr, wiz.UserConfigParams{DftDim: &dim, WhiteRange: &[2]int{2700, 5000}, StartupMode: "x"}, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	u, _ = wiz.GetUserConfig(ctx, addr, 3*time.Second)
	if *u.DftDim != 70 || !reflect.DeepEqual(u.WhiteRange, []int{2700, 5000}) || u.Raw["startupMode"] != "x" {
		t.Errorf("stored user config %+v", u.Raw)
	}
	for _, p := range []map[string]any{{"dftDim": 0}, {"whiteRange": []int{5000, 2700}}, {"pwmRange": []int{1}}, {"colour": 1}, {}} {
		if _, err := wiz.Request(ctx, addr, wiz.MethodSetUserConfig, p, 3*time.Second); !refused(t, err, wiz.CodeInvalidParams) {
			t.Errorf("setUserConfig %v = %v", p, err)
		}
	}

	raw, err := wiz.GetWifiConfig(ctx, addr, 3*time.Second)
	if err != nil || len(raw) < 3 || raw[0] != '"' {
		t.Errorf("wifi config %s %v", raw, err)
	}

	if _, err := wiz.SetSystemConfig(ctx, addr, map[string]any{"fs": 1}, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := wiz.SetModelConfig(ctx, addr, map[string]any{"confTs": 1, "wcr": 30}, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if r := s.Requests(mac, wiz.MethodSetSystemConfig); len(r) != 1 || r[0]["fs"] != 1.0 {
		t.Errorf("setSystemConfig recorded %v", r)
	}
	if r := s.Requests(mac, wiz.MethodSetModelConfig); len(r) != 1 || r[0]["wcr"] != 30.0 {
		t.Errorf("setModelConfig recorded %v", r)
	}
	if _, err := wiz.Request(ctx, addr, wiz.MethodSetSystemConfig, map[string]any{}, 3*time.Second); !refused(t, err, wiz.CodeInvalidParams) {
		t.Errorf("empty setSystemConfig = %v", err)
	}
	// Recorded only: the module does not change.
	raw, _ = wiz.Request(ctx, addr, wiz.MethodGetSystemConfig, nil, 3*time.Second)
	if c, _ := wiz.ParseSystemConfig(raw); c.ModuleName != "ESP01_SHRGB1C_31" || c.FwOtaStatus == nil {
		t.Errorf("system config %s", raw)
	}
}

// pushListener registers a socket for mac's pushes and returns them.
func pushListener(t *testing.T, s *sim.Sim, mac string) chan wiz.Push {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	pushes := make(chan wiz.Push, 16)
	go func() { _ = wiz.ServePush(conn, phoneMAC, func(p wiz.Push) { pushes <- p }) }()
	dev, _ := net.ResolveUDPAddr("udp4", s.Addr(mac))
	if err := wiz.Register(conn, dev, phoneMAC, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return s.Registrations(mac) >= 1 })
	return pushes
}

func silent(addr string) bool {
	_, err := wiz.Request(context.Background(), addr, wiz.MethodGetPilot, nil, 150*time.Millisecond)
	return err != nil
}

func TestRebootAndReset(t *testing.T) {
	s := sim.New()
	defer s.Close()
	s.SetRebootTime(400 * time.Millisecond)
	const mac = "a8bb50e4c101"
	addr := s.Add(sim.Device{MAC: mac, Module: "ESP01_SHRGB1C_31", Firmware: "1.28.0", State: sim.State{On: true, Dimming: 50, R: 255}})
	pushes := pushListener(t, s, mac)
	ctx := context.Background()

	s.SetState(mac, func(st *sim.State) { st.Dimming = 90 })
	if p := next(t, pushes); p.Method != wiz.MethodSyncPilot {
		t.Fatalf("push %+v", p)
	}
	if err := wiz.Reboot(ctx, addr, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if !silent(addr) {
		t.Error("the device answered while restarting")
	}
	if p := next(t, pushes); p.Method != wiz.MethodFirstBeat || p.MAC != mac || p.FwVersion != "1.28.0" {
		t.Errorf("after reboot %+v", p)
	}
	if silent(addr) || s.State(mac).Dimming != 90 {
		t.Errorf("after reboot: silent %v, state %+v", silent(addr), s.State(mac))
	}

	dim := 40
	if err := wiz.SetUserConfig(ctx, addr, wiz.UserConfigParams{DftDim: &dim}, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := wiz.Reset(ctx, addr, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	// A reset device leaves the network until it is set up again.
	time.Sleep(600 * time.Millisecond)
	if !silent(addr) {
		t.Error("a reset device still answers")
	}
	s.SetOffline(mac, false)
	if st := s.State(mac); st.Dimming != 50 || st.R != 255 {
		t.Errorf("state after reset %+v", st)
	}
	if u, err := wiz.GetUserConfig(ctx, addr, 3*time.Second); err != nil || *u.DftDim != 100 {
		t.Errorf("user config after reset %+v %v", u.DftDim, err)
	}
	if s.Registered(mac) {
		t.Error("a reset device kept its push registration")
	}
}

func TestFirmwareUpdate(t *testing.T) {
	s := sim.New()
	defer s.Close()
	s.SetRebootTime(300 * time.Millisecond)
	const mac = "a8bb50e4c101"
	addr := s.Add(sim.Device{MAC: mac, Module: "ESP01_SHRGB1C_31", Firmware: "1.28.0", LatestFirmware: "1.32.0", State: sim.State{On: true, Dimming: 50}})
	pushes := pushListener(t, s, mac)
	ctx := context.Background()

	for _, p := range []map[string]any{{"fw": "default", "force": 2}, {"force": 1}, {"fw": 3}} {
		if _, err := wiz.Request(ctx, addr, wiz.MethodUpdateOta, p, 3*time.Second); !refused(t, err, wiz.CodeInvalidParams) {
			t.Errorf("updateOta %v = %v", p, err)
		}
	}
	if _, err := wiz.UpdateFirmware(ctx, addr, "", true, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	var statuses []int
	for {
		p := next(t, pushes)
		if p.Method == wiz.MethodFirstBeat {
			if p.FwVersion != "1.32.0" {
				t.Errorf("first beat after the update %+v", p)
			}
			break
		}
		if p.Method != wiz.MethodUpdateOta || p.UpdateStatus == nil || p.From.IP.String() != "127.0.0.1" {
			t.Fatalf("push %+v", p)
		}
		statuses = append(statuses, *p.UpdateStatus)
	}
	if len(statuses) == 0 {
		t.Error("no progress pushes")
	}
	raw, _ := wiz.Request(ctx, addr, wiz.MethodGetSystemConfig, nil, 3*time.Second)
	if c, _ := wiz.ParseSystemConfig(raw); c.FwVersion != "1.32.0" || *c.FwOtaStatus != 0 {
		t.Errorf("system config after the update %s", raw)
	}
	// A named version is installed as named.
	if _, err := wiz.UpdateFirmware(ctx, addr, "1.33.1", false, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		raw, err := wiz.Request(ctx, addr, wiz.MethodGetSystemConfig, nil, 200*time.Millisecond)
		c, _ := wiz.ParseSystemConfig(raw)
		return err == nil && c.FwVersion == "1.33.1"
	})
}

func TestExtendedDemo(t *testing.T) {
	s := sim.NewExtendedDemo()
	defer s.Close()
	found, err := wiz.Discover(context.Background(), s.BroadcastAddr(), phoneMAC, 1500*time.Millisecond)
	if err != nil || len(found) != len(sim.ExtendedDemo()) || len(found) != 7 {
		t.Fatalf("found %v %v", found, err)
	}
	classes := map[wiz.Class]int{}
	for _, d := range sim.ExtendedDemo() {
		c, _ := wiz.ClassOf(d.Module)
		classes[c]++
	}
	if classes[wiz.ClassFanDim] != 1 || classes[wiz.ClassFanTW] != 1 || !wiz.FeaturesOf("ESP20_DHRGB_01").DualHead {
		t.Errorf("classes %v", classes)
	}
	plain := sim.NewDemo()
	defer plain.Close()
	if len(sim.Demo()) != 4 || len(plain.MACs()) != 4 {
		t.Error("the plain demo changed")
	}
}
