// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cB-Sigma/wiz"
	"github.com/cB-Sigma/wiz-sim/sim"
)

func TestRunner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan *sim.Sim, 1)
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, []string{"-toggle", "50ms", "-reboot", "100ms"}, &out, func(s *sim.Sim) { got <- s })
	}()
	var s *sim.Sim
	select {
	case s = <-got:
	case err := <-done:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("not ready")
	}
	found, err := wiz.Discover(ctx, s.BroadcastAddr(), "02aabbccddee", time.Second)
	if err != nil || len(found) != 7 {
		t.Fatalf("discovery: %v %v", found, err)
	}
	raw, err := wiz.Request(ctx, found["a8bb50e4c104"], wiz.MethodGetPower, nil, 3*time.Second)
	if err != nil || !strings.Contains(string(raw), "61500") {
		t.Errorf("getPower = %s %v", raw, err)
	}
	m, err := wiz.GetModelConfig(ctx, found["a8bb50e4c106"], 3*time.Second)
	if err != nil || wiz.FanSpeedMax(m, wiz.UserConfig{}) != 5 {
		t.Errorf("fan model config %+v %v", m.FanSpeed, err)
	}
	// -reboot shortens the restart.
	if err := wiz.Reboot(ctx, found["a8bb50e4c105"], 3*time.Second); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := wiz.Request(ctx, found["a8bb50e4c105"], wiz.MethodGetPilot, nil, time.Second); err != nil {
		t.Errorf("after a reboot: %v", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ESP56_SHTW3_01") || !strings.Contains(out.String(), "discovery broadcasts to") {
		t.Errorf("output = %s", out.String())
	}
}

func TestRunnerFlags(t *testing.T) {
	for _, args := range [][]string{{"-host", "nowhere"}, {"-port", "70000"}, {"-bogus"}, {"-reboot", "-1s"}} {
		if err := run(context.Background(), args, &bytes.Buffer{}, nil); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}
