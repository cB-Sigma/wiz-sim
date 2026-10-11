// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package sim_test

// Integration tests of github.com/cB-Sigma/wiz against the simulated
// lights: discovery, push registration, pushes and requests over UDP on
// the loopback interface. They moved here from the library so that the
// library never imports its simulator.

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/cB-Sigma/wiz"

	"github.com/cB-Sigma/wiz-sim/sim"
)

const phoneMAC = "02aabbccddee"

// Find the lights, read one and switch it to a scene.
func Example_discoverAndSetScene() {
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
}

func TestDiscover(t *testing.T) {
	s := sim.NewDemo()
	defer s.Close()
	found, err := wiz.Discover(context.Background(), s.BroadcastAddr(), phoneMAC, 1500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 4 {
		t.Fatalf("found %v", found)
	}
	for _, mac := range s.MACs() {
		if found[mac] != s.Addr(mac) {
			t.Errorf("%s at %q, want %s", mac, found[mac], s.Addr(mac))
		}
	}
	// Discovery is not a push registration.
	if n := s.Registrations("a8bb50e4c101"); n != 0 {
		t.Errorf("registrations after discovery = %d", n)
	}
}

func TestRegisterAndServePush(t *testing.T) {
	s := sim.NewDemo()
	defer s.Close()
	const mac = "a8bb50e4c101"
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	pushes := make(chan wiz.Push, 8)
	done := make(chan error, 1)
	go func() { done <- wiz.ServePush(conn, phoneMAC, func(p wiz.Push) { pushes <- p }) }()

	dev, _ := net.ResolveUDPAddr("udp4", s.Addr(mac))
	if wiz.LocalIPFor(dev) != "127.0.0.1" {
		t.Errorf("LocalIPFor = %q", wiz.LocalIPFor(dev))
	}
	if err := wiz.Register(conn, dev, phoneMAC, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return s.Registrations(mac) == 1 })
	reg := s.LastRegistration(mac)
	if reg["register"] != true || reg["phoneMac"] != phoneMAC || reg["phoneIp"] != "127.0.0.1" {
		t.Errorf("registration = %v", reg)
	}

	// The registration's answer reaches the push socket and is skipped;
	// the state change arrives as a syncPilot from a port of the device's
	// own, and is acknowledged.
	s.SetState(mac, func(st *sim.State) { st.On = false })
	p := next(t, pushes)
	if p.Method != wiz.MethodSyncPilot || p.MAC != mac || p.Pilot.State == nil || *p.Pilot.State {
		t.Errorf("push = %+v", p)
	}
	_, apiPort, _ := net.SplitHostPort(s.Addr(mac))
	if strconv.Itoa(p.From.Port) == apiPort {
		t.Error("the simulated push came from the API port; it should come from a port of its own")
	}
	if got := wiz.PushedAddr(p.From, s.Addr(mac)); got != s.Addr(mac) {
		t.Errorf("PushedAddr = %s, want %s", got, s.Addr(mac))
	}
	waitFor(t, func() bool { return s.Acks() >= 1 })

	// A device powering up announces itself.
	s.FirstBeat(mac)
	p = next(t, pushes)
	if p.Method != wiz.MethodFirstBeat || p.MAC != mac {
		t.Errorf("first beat = %+v", p)
	}

	// Requests still reach the API port after a push.
	if _, err := wiz.Request(context.Background(), wiz.PushedAddr(p.From, s.Addr(mac)), wiz.MethodSetPilot, map[string]any{"state": true}, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if !s.State(mac).On {
		t.Error("setPilot after a push did not arrive")
	}

	conn.Close()
	if err := <-done; err == nil {
		t.Error("ServePush returned nil after the socket closed")
	}
}

func next(t *testing.T, c chan wiz.Push) wiz.Push {
	t.Helper()
	select {
	case p := <-c:
		return p
	case <-time.After(5 * time.Second):
		t.Fatal("no push")
	}
	return wiz.Push{}
}

func waitFor(t *testing.T, f func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if f() {
			return
		}
	}
	t.Fatal("condition not met in 5s")
}
