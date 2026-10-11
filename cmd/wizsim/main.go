// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

// Command wizsim serves simulated WiZ lights, a plug and ceiling fans on
// the local UDP API, so a WiZ client can be tried without devices. Each
// device has its own UDP port on -host (real devices each have 38899 on
// their own IP), and a further port stands in for the broadcast address
// of discovery:
//
//	a8bb50e4c101  ESP01_SHRGB1C_31  colour bulb (firmware update to 1.32.0 offered)
//	a8bb50e4c102  ESP56_SHTW3_01    tunable-white bulb
//	a8bb50e4c103  ESP05_SHDW9_01    dimmable-white bulb
//	a8bb50e4c104  ESP10_SOCKET_06   plug with power reading
//	a8bb50e4c105  ESP03_FANDIMS_31  ceiling fan with a dimmable light, six speeds
//	a8bb50e4c106  ESP20_DDTW1_01    ceiling fan with a tunable-white light, five speeds
//	a8bb50e4c107  ESP20_DHRGB_01    dual-head colour lamp
//
// For example:
//
//	wizsim -host 127.0.0.1 -port 38899
//
// gives the devices ports 38899 to 38905. With -toggle, the colour bulb
// switches every interval, so registered clients receive pushes. -reboot
// sets how long a device stays silent after reboot or a firmware update.
// -bcast-port puts the broadcast socket on a given port; -push-port makes
// devices push to each registration's phoneIp at that port, as real
// devices push to port 38900.
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

var demo = sim.ExtendedDemo()

// run serves until ctx ends. ready, when not nil, receives the simulator
// once it listens.
func run(ctx context.Context, args []string, out io.Writer, ready func(*sim.Sim)) error {
	fs := flag.NewFlagSet("wizsim", flag.ContinueOnError)
	fs.SetOutput(out)
	host := fs.String("host", "127.0.0.1", "IPv4 address the devices listen on")
	port := fs.Int("port", 0, "UDP port of the first device; the others follow (0: free ports)")
	toggle := fs.Duration("toggle", 0, "switch the colour bulb on and off at this interval (0: never)")
	reboot := fs.Duration("reboot", 2*time.Second, "how long a device stays silent after reboot or a firmware update")
	bcastPort := fs.Int("bcast-port", 0, "UDP port standing in for the broadcast address (0: a free port)")
	pushPort := fs.Int("push-port", 0, "push to each registration's phoneIp at this port, as devices push to 38900 (0: to the registration's source)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	for name, v := range map[string]int{"-bcast-port": *bcastPort, "-push-port": *pushPort} {
		if v < 0 || v > 65535 {
			return fmt.Errorf("%s %d is out of range", name, v)
		}
	}
	if *port < 0 || *port > 65535-len(demo) {
		return fmt.Errorf("-port %d is out of range", *port)
	}
	if *reboot < 0 {
		return fmt.Errorf("-reboot %s is negative", *reboot)
	}
	s, err := sim.NewHostAt(*host, *bcastPort)
	if err != nil {
		return err
	}
	defer s.Close()
	s.SetRebootTime(*reboot)
	s.SetPushPort(*pushPort)
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
