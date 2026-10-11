// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package sim_test

import (
	"context"
	"errors"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/cB-Sigma/wiz"
	"github.com/cB-Sigma/wiz-sim/sim"
)

const dhMAC = "a8bb50e4c108"

func perHeadDevice() sim.Device {
	return sim.Device{MAC: dhMAC, Module: "ESP20_DHRGB_01", Firmware: "1.38.0", PerHead: true,
		State: sim.State{On: true, Dimming: 25, Temp: 2700}, Head2: sim.State{Dimming: 75, Temp: 4000}}
}

func TestPerHeadControl(t *testing.T) {
	s := sim.New()
	defer s.Close()
	addr := s.Add(perHeadDevice())
	ctx := context.Background()

	p1, err := wiz.GetHeadPilot(ctx, addr, wiz.HeadFirst, 3*time.Second)
	if err != nil || *p1.Dimming != 25 || !*p1.State || p1.Ratio != nil {
		t.Fatalf("head 1 %+v %v", p1, err)
	}
	p2, err := wiz.GetHeadPilot(ctx, addr, wiz.HeadSecond, 3*time.Second)
	if err != nil || *p2.Dimming != 75 || *p2.State {
		t.Fatalf("head 2 %+v %v", p2, err)
	}
	// A plain getPilot answers with head 1, untagged.
	raw, _ := wiz.Request(ctx, addr, wiz.MethodGetPilot, nil, 3*time.Second)
	if p, _ := wiz.ParsePilot(raw); p.Head() != 0 || *p.Dimming != 25 {
		t.Errorf("plain getPilot %s", raw)
	}

	pushes := pushListener(t, s, dhMAC)
	params, _ := wiz.HeadParams(wiz.HeadSecond, map[string]any{"dimming": 50})
	if _, err := wiz.Request(ctx, addr, wiz.MethodSetPilot, params, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if st := s.HeadState(dhMAC, 2); !st.On || st.Dimming != 50 {
		t.Errorf("head 2 after setPilot %+v", st)
	}
	if st := s.HeadState(dhMAC, 1); st.Dimming != 25 {
		t.Errorf("head 1 changed %+v", st)
	}
	if p := next(t, pushes); p.Pilot.Head() != wiz.HeadSecond || *p.Pilot.Dimming != 50 {
		t.Errorf("push %+v", p.Pilot.Raw)
	}
	// Without devices both heads change, and each pushes.
	if _, err := wiz.Request(ctx, addr, wiz.MethodSetPilot, map[string]any{"state": false}, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	got := map[int]bool{}
	for i := 0; i < 2; i++ {
		got[next(t, pushes).Pilot.Head()] = true
	}
	if !got[1] || !got[2] || s.HeadState(dhMAC, 1).On || s.HeadState(dhMAC, 2).On {
		t.Errorf("both heads: pushes %v, states %+v %+v", got, s.HeadState(dhMAC, 1), s.HeadState(dhMAC, 2))
	}
	s.SetHeadState(dhMAC, 1, func(st *sim.State) { st.Dimming = 90 })
	if p := next(t, pushes); p.Pilot.Head() != 1 || *p.Pilot.Dimming != 90 {
		t.Errorf("app change push %+v", p.Pilot.Raw)
	}

	// Refused: head 3, ratio on a lamp with a state per head, selector 2,
	// devices on a lamp without a state per head.
	var de *wiz.DeviceError
	for _, p := range []map[string]any{{"devices": 3, "state": true}, {"ratio": 40}} {
		if _, err := wiz.Request(ctx, addr, wiz.MethodSetPilot, p, 3*time.Second); !errors.As(err, &de) {
			t.Errorf("setPilot %v: %v", p, err)
		}
	}
	if _, err := wiz.Request(ctx, addr, wiz.MethodGetPilot, map[string]any{"devices": 2}, 3*time.Second); !errors.As(err, &de) {
		t.Errorf("selector 2: %v", err)
	}
	ratioLamp := s.Add(sim.Device{MAC: "a8bb50e4c109", Module: "ESP20_DHRGB_01", Firmware: "1.21.40", State: sim.State{On: true, Dimming: 80, Ratio: 50}})
	if _, err := wiz.Request(ctx, ratioLamp, wiz.MethodSetPilot, map[string]any{"devices": 1, "state": true}, 3*time.Second); !errors.As(err, &de) {
		t.Errorf("devices on a ratio lamp: %v", err)
	}
	// A lamp without a state per head ignores the selector.
	if _, err := wiz.GetHeadPilot(ctx, ratioLamp, wiz.HeadFirst, 3*time.Second); !errors.Is(err, wiz.ErrNoHeadState) {
		t.Errorf("ratio lamp head pilot: %v", err)
	}
}

func TestPulseMethod(t *testing.T) {
	s := sim.NewDemo()
	defer s.Close()
	ctx := context.Background()
	const bulb, plug = "a8bb50e4c101", "a8bb50e4c104"
	before := s.State(bulb)
	if err := wiz.Pulse(ctx, s.Addr(bulb), -15, 300*time.Millisecond, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if s.Pulses(bulb) != 1 || s.State(bulb) != before {
		t.Errorf("pulses %d, state %+v", s.Pulses(bulb), s.State(bulb))
	}
	var de *wiz.DeviceError
	if err := wiz.Pulse(ctx, s.Addr(plug), 10, time.Second, 3*time.Second); !errors.As(err, &de) {
		t.Errorf("plug pulse: %v", err)
	}
	for _, p := range []map[string]any{{"delta": 0, "duration": 100}, {"delta": 101, "duration": 100}, {"delta": 10}, {"delta": 10, "duration": 0}} {
		if _, err := wiz.Request(ctx, s.Addr(bulb), wiz.MethodPulse, p, 3*time.Second); !errors.As(err, &de) {
			t.Errorf("pulse %v: %v", p, err)
		}
	}
}

// With a push port set, pushes go to the registration's phoneIp at that
// port, as real devices push to port 38900, not to the registration's
// source.
func TestPushToPhoneIP(t *testing.T) {
	s := sim.NewDemo()
	defer s.Close()
	const mac = "a8bb50e4c101"
	listen, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listen.Close()
	s.SetPushPort(listen.LocalAddr().(*net.UDPAddr).Port)
	pushes := make(chan wiz.Push, 4)
	go func() { _ = wiz.ServePush(listen, phoneMAC, func(p wiz.Push) { pushes <- p }) }()

	from, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer from.Close()
	dev, _ := net.ResolveUDPAddr("udp4", s.Addr(mac))
	if err := wiz.Register(from, dev, phoneMAC, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return s.Registrations(mac) >= 1 })
	s.SetState(mac, func(st *sim.State) { st.Dimming = 33 })
	if p := next(t, pushes); p.Method != wiz.MethodSyncPilot || *p.Pilot.Dimming != 33 {
		t.Errorf("push %+v", p)
	}
}

func TestNewHostAt(t *testing.T) {
	free, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	port := free.LocalAddr().(*net.UDPAddr).Port
	_ = free.Close()
	s, err := sim.NewHostAt("127.0.0.1", port)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.BroadcastAddr() != "127.0.0.1:"+strconv.Itoa(port) {
		t.Errorf("broadcast at %s", s.BroadcastAddr())
	}
	s.Add(sim.Demo()[0])
	found, err := wiz.Discover(context.Background(), s.BroadcastAddr(), phoneMAC, 500*time.Millisecond)
	if err != nil || len(found) != 1 {
		t.Errorf("discovered %v %v", found, err)
	}
}

func TestBroadcastAnswers(t *testing.T) {
	s := sim.NewDemo()
	defer s.Close()
	s.SetBroadcastAnswers(true)
	found, err := wiz.Discover(context.Background(), s.BroadcastAddr(), phoneMAC, 500*time.Millisecond)
	if err != nil || len(found) != 4 {
		t.Fatalf("discovered %v %v", found, err)
	}
	for mac, addr := range found {
		if addr != s.BroadcastAddr() {
			t.Errorf("%s answered from %s, not the broadcast socket", mac, addr)
		}
	}
}
