// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package sim_test

import (
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/cB-Sigma/wiz-sim/sim"
)

// ask sends one request and returns the first answer, from any port
// (devices answer a broadcast from their own socket).
func ask(t *testing.T, addr, msg string) map[string]any {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	to, _ := net.ResolveUDPAddr("udp4", addr)
	if _, err := c.WriteToUDP([]byte(msg), to); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 2048)
	n, _, err := c.ReadFromUDP(buf)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(buf[:n], &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSim(t *testing.T) {
	s, err := sim.NewHost("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	addr := s.Add(sim.Device{MAC: "a8bb50e4c1aa", Module: "ESP05_SHDW9_01", Firmware: "1.0.0", State: sim.State{Dimming: 50}})

	m := ask(t, addr, `{"method":"getSystemConfig","params":{}}`)
	if r, _ := m["result"].(map[string]any); r["moduleName"] != "ESP05_SHDW9_01" || r["mac"] != "a8bb50e4c1aa" {
		t.Errorf("getSystemConfig = %v", m)
	}
	// A dimmable white light refuses a colour temperature and colours.
	for _, p := range []string{`{"temp":3000}`, `{"r":255,"g":0,"b":0}`, `{"dimming":5}`} {
		if m := ask(t, addr, `{"method":"setPilot","params":`+p+`}`); m["error"] == nil {
			t.Errorf("setPilot %s = %v", p, m)
		}
	}
	if m := ask(t, addr, `{"method":"setPilot","params":{"dimming":70}}`); m["error"] != nil {
		t.Errorf("setPilot dimming = %v", m)
	}
	if st := s.State("a8bb50e4c1aa"); !st.On || st.Dimming != 70 || len(s.SetPilots("a8bb50e4c1aa")) != 1 {
		t.Errorf("state %+v, setPilots %v", st, s.SetPilots("a8bb50e4c1aa"))
	}
	// Plugs only answer getPower.
	if m := ask(t, addr, `{"method":"getPower"}`); m["error"] == nil {
		t.Errorf("getPower on a light = %v", m)
	}
	// Unknown methods are refused; an offline device is silent.
	if m := ask(t, addr, `{"method":"reboot"}`); m["error"] == nil {
		t.Errorf("reboot = %v", m)
	}
	if s.Calls("setPilot") != 4 {
		t.Errorf("setPilot calls = %d", s.Calls("setPilot"))
	}
	s.SetOffline("a8bb50e4c1aa", true)
	c, _ := net.Dial("udp4", addr)
	defer c.Close()
	_, _ = c.Write([]byte(`{"method":"getPilot"}`))
	_ = c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, err := c.Read(make([]byte, 64)); err == nil {
		t.Error("an offline device answered")
	}
	// The broadcast socket hands a request to every device.
	s.SetOffline("a8bb50e4c1aa", false)
	if m := ask(t, s.BroadcastAddr(), `{"method":"registration","params":{"register":false,"phoneMac":"02aabbccddee","phoneIp":"0.0.0.0"}}`); m["result"] == nil {
		t.Errorf("broadcast discovery = %v", m)
	}
	if s.Registrations("a8bb50e4c1aa") != 0 {
		t.Error("the discovery form counted as a push registration")
	}
	if _, err := sim.NewHost("not-an-ip"); err == nil {
		t.Error("NewHost accepted a bad address")
	}
}

func Example() {
	s := sim.NewDemo() // a colour bulb, a tunable-white bulb, a dimmable-white bulb and a plug
	defer s.Close()
	fmt.Println(len(s.MACs()), "devices; plug on:", s.State("a8bb50e4c104").On)
	s.SetState("a8bb50e4c101", func(st *sim.State) { st.On = false }) // pushed to registered addresses
	fmt.Println(s.State("a8bb50e4c101").On)
	// Output:
	// 4 devices; plug on: true
	// false
}
