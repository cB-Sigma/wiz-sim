// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package interop

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/cB-Sigma/wiz"
	"github.com/cB-Sigma/wiz-sim/sim"
)

// The peer: pywizlight at this version, in this image.
const (
	image             = "python:3.12.10-slim"
	pywizlightPackage = "pywizlight==0.6.6"
)

// simHost is where the simulator listens so that the container reaches it
// at host.docker.internal (see doc.go).
func simHost() string {
	if runtime.GOOS == "darwin" {
		return "127.0.0.1"
	}
	return "0.0.0.0"
}

func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

// startPeer starts the Python container with the push port published on
// pushPort of the host's 127.0.0.1, installs pywizlight and returns the
// container's name.
func startPeer(t *testing.T, pushPort int) string {
	t.Helper()
	if os.Getenv("SIGMA_WIZ_INTEROP") != "1" {
		t.Skip("runs pywizlight in Docker; set SIGMA_WIZ_INTEROP=1 to run")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not on PATH")
	}
	td, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("sigma-wiz-pywizlight-%d", os.Getpid())
	out, err := exec.Command("docker", "run", "-d", "--name", name,
		"--add-host", "host.docker.internal:host-gateway",
		"-p", fmt.Sprintf("127.0.0.1:%d:38900/udp", pushPort),
		"-v", td+":/sigma:ro", image, "sleep", "3600").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "container", "rm", "-f", name).Run() })
	out, err = exec.Command("docker", "exec", name, "pip", "install", "--no-cache-dir",
		"--disable-pip-version-check", "--root-user-action=ignore", pywizlightPackage).CombinedOutput()
	if err != nil {
		t.Fatalf("pip install %s: %v: %s", pywizlightPackage, err, out)
	}
	return name
}

type line struct {
	Check  string         `json:"check"`
	OK     bool           `json:"ok"`
	Detail string         `json:"detail"`
	Need   string         `json:"need"`
	Args   map[string]any `json:"args"`
}

// runScript runs the client script; act is called for each {"need": …}
// and the script continues when it returns. It returns the checks,
// failing the test for each one that failed.
func runScript(t *testing.T, name string, act func(need string, args map[string]any), args ...string) map[string]line {
	t.Helper()
	cmd := exec.Command("docker", append([]string{"exec", "-i", name, "python", "-I", "/sigma/pywizlight_client.py"}, args...)...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	got := map[string]line{}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		var l line
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			t.Logf("pywizlight: %s", sc.Text())
			continue
		}
		if l.Need != "" {
			act(l.Need, l.Args)
			_, _ = io.WriteString(stdin, "go\n")
			continue
		}
		got[l.Check] = l
		if l.OK {
			t.Logf("PASS %s: %s", l.Check, l.Detail)
		} else {
			t.Errorf("FAIL %s: %s", l.Check, l.Detail)
		}
	}
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Errorf("pywizlight client: %v\n%s", err, stderr.String())
	}
	if _, ok := got["pywizlight"]; !ok {
		t.Fatalf("the pywizlight client reported nothing\n%s", stderr.String())
	}
	return got
}

func num(v any) int {
	f, _ := v.(float64)
	return int(f)
}

func within1(a, b int) bool { return a-b <= 1 && b-a <= 1 }

// checkColour compares the last setPilot pywizlight sent with want.
func checkColour(t *testing.T, label string, sent map[string]any, want wiz.RGBCW) {
	t.Helper()
	_, hasC := sent["c"]
	if hasC || !within1(num(sent["r"]), want.R) || !within1(num(sent["g"]), want.G) || !within1(num(sent["b"]), want.B) || num(sent["w"]) != want.W {
		t.Errorf("FAIL %s: pywizlight sent %v, the wiz module gives %+v", label, sent, want)
		return
	}
	t.Logf("PASS %s: pywizlight sent %v, the wiz module gives %+v", label, sent, want)
}

func waitFor(t *testing.T, f func() bool) {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if f() {
			return
		}
	}
	t.Error("timed out waiting")
}

// pywizlight's client against the extended demo and a dual-head lamp with
// a state per head.
func TestPywizlightAgainstSimulator(t *testing.T) {
	pushPort := freeUDPPort(t)
	name := startPeer(t, pushPort)
	s, err := sim.NewHostAt(simHost(), wiz.DevicePort)
	if err != nil {
		t.Fatalf("the simulator needs UDP port %d free on the host: %v", wiz.DevicePort, err)
	}
	defer s.Close()
	s.SetRebootTime(300 * time.Millisecond)
	s.SetPushPort(pushPort)
	// Docker's UDP forwarding passes back only answers from the port the
	// container sent to (38899), as on a LAN, where each device answers
	// from its own 38899.
	s.SetBroadcastAnswers(true)
	kinds := []string{"rgb", "tw", "dw", "socket", "fandim", "fantw", "dh"}
	devs := sim.ExtendedDemo()
	devs = append(devs, sim.Device{MAC: "a8bb50e4c108", Module: "ESP20_DHRGB_01", Firmware: "1.38.0", PerHead: true,
		State: sim.State{On: true, Dimming: 25, Temp: 2700}, Head2: sim.State{Dimming: 75, Temp: 4000}})
	kinds = append(kinds, "perhead")
	type devArg struct {
		MAC  string `json:"mac"`
		Port int    `json:"port"`
		Kind string `json:"kind"`
	}
	var list []devArg
	macOf := map[string]string{}
	for i, d := range devs {
		addr := s.Add(d)
		_, p, _ := net.SplitHostPort(addr)
		port, _ := strconv.Atoi(p)
		list = append(list, devArg{MAC: d.MAC, Port: port, Kind: kinds[i]})
		macOf[kinds[i]] = d.MAC
	}
	js, _ := json.Marshal(list)

	lastSet := func(mac string) map[string]any {
		sets := s.SetPilots(mac)
		if len(sets) == 0 {
			return nil
		}
		return sets[len(sets)-1]
	}
	got := runScript(t, name, func(need string, a map[string]any) {
		switch need {
		case "check-hs":
			hue, sat := a["hue"].(float64), a["sat"].(float64)
			want, err := wiz.HSToRGBWPywizlight(hue, sat/100)
			if err != nil {
				t.Error(err)
				return
			}
			checkColour(t, fmt.Sprintf("hs %v,%v", hue, sat), lastSet(macOf["rgb"]), want)
		case "check-rgb":
			want, err := wiz.RGBToRGBWPywizlight(num(a["r"]), num(a["g"]), num(a["b"]))
			if err != nil {
				t.Error(err)
				return
			}
			checkColour(t, fmt.Sprintf("rgb %v,%v,%v", a["r"], a["g"], a["b"]), lastSet(macOf["rgb"]), want)
		case "check-head":
			head := num(a["head"])
			st := s.HeadState(macOf["perhead"], head)
			if st.On != a["on"].(bool) || st.Dimming != num(a["dimming"]) {
				t.Errorf("FAIL head %d after pywizlight's setPilot: %+v (sent %v)", head, st, lastSet(macOf["perhead"]))
			} else {
				t.Logf("PASS head %d: %+v (sent %v)", head, st, lastSet(macOf["perhead"]))
			}
		case "wait-registered":
			mac := a["mac"].(string)
			waitFor(t, func() bool { return s.Registered(mac) })
			t.Logf("registration %v", s.LastRegistration(mac))
		case "change":
			s.SetState(a["mac"].(string), func(st *sim.State) { st.Dimming = num(a["dimming"]) })
		case "change-head":
			s.SetHeadState(a["mac"].(string), num(a["head"]), func(st *sim.State) { st.Dimming = num(a["dimming"]) })
		case "firstbeat":
			s.FirstBeat(a["mac"].(string))
		default:
			t.Errorf("unknown need %q", need)
		}
	}, "--host", "host.docker.internal", "--devices", string(js), "--push-ip", "127.0.0.1")

	for _, c := range []string{"discovery", "bulbtype-rgb", "bulbtype-tw", "bulbtype-dw", "bulbtype-socket", "bulbtype-fandim",
		"bulbtype-fantw", "bulbtype-dh", "bulbtype-perhead", "kelvin-range-rgb", "fan-speed-range", "supported-scenes-dw",
		"rgb", "rgbww", "colortemp", "scene-speed", "set-speed", "turn-off", "set-state-keeps-off", "light-switch",
		"tw-colortemp", "tw-refuses-rgb", "dw-brightness", "dw-refuses-colortemp", "plug-get-power", "plug-pilot-power",
		"bulb-no-power", "method-not-found", "fan-on", "fan-breeze-reverse", "fan-switch-off", "fan-speed-limit", "fantw",
		"ratio", "per-head-poll", "push-start", "push-syncpilot", "push-firstbeat", "push-per-head", "reboot"} {
		if _, ok := got[c]; !ok {
			t.Errorf("check %s did not run", c)
		}
	}
}
