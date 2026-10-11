// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

// Command wizsim serves simulated WiZ lights and a plug on the local UDP
// API, so a WiZ client can be tried without devices. Each device has its
// own UDP port on -host (real devices each have 38899 on their own IP),
// and a further port stands in for the broadcast address of discovery:
//
//	a8bb50e4c101  ESP01_SHRGB1C_31  colour bulb
//	a8bb50e4c102  ESP56_SHTW3_01    tunable-white bulb
//	a8bb50e4c103  ESP05_SHDW9_01    dimmable-white bulb
//	a8bb50e4c104  ESP10_SOCKET_06   plug with power reading
//
// For example:
//
//	wizsim -host 127.0.0.1 -port 38899
//
// gives the devices ports 38899 to 38902. With -toggle, the colour bulb
// switches every interval, so registered clients receive pushes.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cB-Sigma/wiz-sim/sim"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, nil); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "wizsim:", err)
		os.Exit(1)
	}
}

var demo = []sim.Device{
	{MAC: "a8bb50e4c101", Module: "ESP01_SHRGB1C_31", Firmware: "1.28.0", State: sim.State{On: true, Dimming: 80, R: 255, G: 120, B: 0}},
	{MAC: "a8bb50e4c102", Module: "ESP56_SHTW3_01", Firmware: "1.25.0", State: sim.State{On: true, Dimming: 60, Temp: 2700}},
	{MAC: "a8bb50e4c103", Module: "ESP05_SHDW9_01", Firmware: "1.22.0", State: sim.State{Dimming: 100, SceneID: 14}},
	{MAC: "a8bb50e4c104", Module: "ESP10_SOCKET_06", Firmware: "1.26.1", State: sim.State{On: true}, PowerMW: 61500},
}

// run serves until ctx ends. ready, when not nil, receives the simulator
// once it listens.
func run(ctx context.Context, args []string, out io.Writer, ready func(*sim.Sim)) error {
	fs := flag.NewFlagSet("wizsim", flag.ContinueOnError)
	fs.SetOutput(out)
	host := fs.String("host", "127.0.0.1", "IPv4 address the devices listen on")
	port := fs.Int("port", 0, "UDP port of the first device; the others follow (0: free ports)")
	toggle := fs.Duration("toggle", 0, "switch the colour bulb on and off at this interval (0: never)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *port < 0 || *port > 65535-len(demo) {
		return fmt.Errorf("-port %d is out of range", *port)
	}
	s, err := sim.NewHost(*host)
	if err != nil {
		return err
	}
	defer s.Close()
	for i, d := range demo {
		p := 0
		if *port != 0 {
			p = *port + i
		}
		a, err := s.AddAt(d, p)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "wizsim: %s %-16s on udp %s\n", d.MAC, d.Module, a)
	}
	fmt.Fprintf(out, "wizsim: discovery broadcasts to udp %s\n", s.BroadcastAddr())
	if ready != nil {
		ready(s)
	}
	var tick <-chan time.Time
	if *toggle > 0 {
		t := time.NewTicker(*toggle)
		defer t.Stop()
		tick = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick:
			s.SetState(demo[0].MAC, func(st *sim.State) { st.On = !st.On })
		}
	}
}
