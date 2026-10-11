// Package sim simulates WiZ lights and plugs on their local UDP API for
// tests and demos: each simulated device answers on its own UDP socket
// (getPilot, setPilot, getSystemConfig, getPower, registration), a shared
// socket stands in for the broadcast address of discovery, and devices
// registered for pushes send syncPilot to the registering address when
// their state changes, from a port of their own as real lights do.
// Sockets are on 127.0.0.1 unless NewHost names another address. Built
// from the publicly described behaviour of the local API; not a WiZ
// product.
package sim

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"
)

// State is a simulated device's light state.
type State struct {
	On      bool
	Dimming int // 10 to 100
	R, G, B int
	C, W    int
	Temp    int // kelvin; 0 in colour or scene mode
	SceneID int
	Speed   int
}

// Device configures a simulated device.
type Device struct {
	MAC      string // 12 lower-case hexadecimal digits
	Module   string // moduleName, such as ESP01_SHRGB1C_31
	Firmware string
	State    State
	// PowerMW is what getPower reports (milliwatts); plugs only.
	PowerMW int
}

type device struct {
	cfg     Device
	conn    *net.UDPConn
	offline bool
	pushTo  []*net.UDPAddr
}

// Sim is a set of simulated devices.
type Sim struct {
	mu      sync.Mutex
	devs    []*device
	bcast   *net.UDPConn
	calls   map[string]int
	sets    map[string][]map[string]any // MAC → setPilot params
	regs    map[string]int              // MAC → registrations with register true
	acks    int
	lastReg map[string]map[string]any
	wg      sync.WaitGroup
	host    net.IP
}

// New returns a simulator on 127.0.0.1 with no devices and its broadcast
// socket open. It panics when the socket cannot be opened.
func New() *Sim {
	s, err := NewHost("127.0.0.1")
	if err != nil {
		panic(err)
	}
	return s
}

// NewHost returns a simulator whose sockets are on the IPv4 address host,
// with no devices and its broadcast socket open on a free port.
func NewHost(host string) (*Sim, error) {
	ip := net.ParseIP(host).To4()
	if ip == nil {
		return nil, fmt.Errorf("sim: %q is not an IPv4 address", host)
	}
	s := &Sim{calls: map[string]int{}, sets: map[string][]map[string]any{}, regs: map[string]int{}, lastReg: map[string]map[string]any{}, host: ip}
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: ip})
	if err != nil {
		return nil, err
	}
	s.bcast = c
	s.wg.Add(1)
	go s.serveBroadcast()
	return s, nil
}

// NewDemo returns a simulator with a colour bulb, a tunable-white bulb, a
// dimmable-white bulb and a plug, the kind of set an Indian home buys as
// "Philips Smart Wi-Fi LED".
func NewDemo() *Sim {
	s := New()
	s.Add(Device{MAC: "a8bb50e4c101", Module: "ESP01_SHRGB1C_31", Firmware: "1.28.0", State: State{On: true, Dimming: 80, R: 255, G: 120, B: 0}})
	s.Add(Device{MAC: "a8bb50e4c102", Module: "ESP56_SHTW3_01", Firmware: "1.25.0", State: State{On: true, Dimming: 60, Temp: 2700}})
	s.Add(Device{MAC: "a8bb50e4c103", Module: "ESP05_SHDW9_01", Firmware: "1.22.0", State: State{On: false, Dimming: 100, SceneID: 14}})
	s.Add(Device{MAC: "a8bb50e4c104", Module: "ESP10_SOCKET_06", Firmware: "1.26.1", State: State{On: true}, PowerMW: 61500})
	return s
}

// Add starts a device on a free port and returns its address
// (host:port). It panics when the socket cannot be opened.
func (s *Sim) Add(d Device) string {
	a, err := s.AddAt(d, 0)
	if err != nil {
		panic(err)
	}
	return a
}

// AddAt starts a device on the given port of the simulator's address (0
// for a free one) and returns its address (host:port).
func (s *Sim) AddAt(d Device, port int) (string, error) {
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: s.host, Port: port})
	if err != nil {
		return "", err
	}
	dv := &device{cfg: d, conn: c}
	s.mu.Lock()
	s.devs = append(s.devs, dv)
	s.mu.Unlock()
	s.wg.Add(1)
	go s.serve(dv)
	return c.LocalAddr().String(), nil
}

// BroadcastAddr is the address to use as the discovery broadcast address.
func (s *Sim) BroadcastAddr() string { return s.bcast.LocalAddr().String() }

// Addr returns a device's address.
func (s *Sim) Addr(mac string) string {
	if d := s.find(mac); d != nil {
		return d.conn.LocalAddr().String()
	}
	return ""
}

// MACs lists the devices.
func (s *Sim) MACs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.devs))
	for i, d := range s.devs {
		out[i] = d.cfg.MAC
	}
	return out
}

// Close stops every socket.
func (s *Sim) Close() {
	s.mu.Lock()
	_ = s.bcast.Close()
	for _, d := range s.devs {
		_ = d.conn.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *Sim) find(mac string) *device {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.devs {
		if d.cfg.MAC == mac {
			return d
		}
	}
	return nil
}

// State returns a device's state.
func (s *Sim) State(mac string) State {
	d := s.find(mac)
	s.mu.Lock()
	defer s.mu.Unlock()
	if d == nil {
		return State{}
	}
	return d.cfg.State
}

// SetState changes a device's state as a wall switch or the phone app
// would, and pushes syncPilot to registered addresses.
func (s *Sim) SetState(mac string, f func(*State)) {
	d := s.find(mac)
	if d == nil {
		return
	}
	s.mu.Lock()
	f(&d.cfg.State)
	s.mu.Unlock()
	s.push(d, "syncPilot")
}

// SetPower sets what a plug reports to getPower (milliwatts).
func (s *Sim) SetPower(mac string, mw int) {
	if d := s.find(mac); d != nil {
		s.mu.Lock()
		d.cfg.PowerMW = mw
		s.mu.Unlock()
	}
}

// SetOffline makes a device ignore every datagram (powered off at the wall).
func (s *Sim) SetOffline(mac string, off bool) {
	if d := s.find(mac); d != nil {
		s.mu.Lock()
		d.offline = off
		s.mu.Unlock()
	}
}

// FirstBeat sends the announcement a device makes when it powers up to
// its registered addresses.
func (s *Sim) FirstBeat(mac string) {
	if d := s.find(mac); d != nil {
		s.push(d, "firstBeat")
	}
}

// Calls counts the requests of a method the devices answered.
func (s *Sim) Calls(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[method]
}

// SetPilots returns the setPilot params a device received, in order.
func (s *Sim) SetPilots(mac string) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.sets[mac]...)
}

// Registrations counts a device's push registrations.
func (s *Sim) Registrations(mac string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.regs[mac]
}

// LastRegistration returns the params of a device's last push registration.
func (s *Sim) LastRegistration(mac string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastReg[mac]
}

// Acks counts syncPilot replies the simulator received.
func (s *Sim) Acks() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.acks
}

func (s *Sim) serveBroadcast() {
	defer s.wg.Done()
	buf := make([]byte, 2048)
	for {
		n, from, err := s.bcast.ReadFromUDP(buf)
		if err != nil {
			return
		}
		var m struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal(buf[:n], &m) != nil {
			continue
		}
		if m.Method == "syncPilot" { // an acknowledgement sent to a pushing device
			continue
		}
		s.mu.Lock()
		devs := append([]*device(nil), s.devs...)
		s.mu.Unlock()
		for _, d := range devs {
			s.handle(d, buf[:n], from)
		}
	}
}

func (s *Sim) serve(d *device) {
	defer s.wg.Done()
	buf := make([]byte, 2048)
	for {
		n, from, err := d.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		s.handle(d, append([]byte(nil), buf[:n]...), from)
	}
}

func (s *Sim) reply(d *device, to *net.UDPAddr, v any) {
	b, _ := json.Marshal(v)
	_, _ = d.conn.WriteToUDP(b, to)
}

func errReply(method string, code int, msg string) map[string]any {
	return map[string]any{"method": method, "env": "pro", "error": map[string]any{"code": code, "message": msg}}
}

func (s *Sim) handle(d *device, b []byte, from *net.UDPAddr) {
	var m struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(b, &m) != nil {
		return
	}
	s.mu.Lock()
	off := d.offline
	if m.Method == "syncPilot" && m.Result != nil {
		s.acks++
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	if off {
		return
	}
	var params map[string]any
	_ = json.Unmarshal(m.Params, &params)
	s.mu.Lock()
	s.calls[m.Method]++
	mac, module, fw, pw := d.cfg.MAC, d.cfg.Module, d.cfg.Firmware, d.cfg.PowerMW
	s.mu.Unlock()
	switch m.Method {
	case "getPilot":
		s.reply(d, from, map[string]any{"method": "getPilot", "env": "pro", "result": s.pilot(d, "")})
	case "getSystemConfig":
		s.reply(d, from, map[string]any{"method": "getSystemConfig", "env": "pro", "result": map[string]any{
			"mac": mac, "homeId": 4242, "roomId": 7, "moduleName": module, "fwVersion": fw, "groupId": 0, "typeId": 0, "ping": 0,
		}})
	case "getPower":
		if len(module) < 6 || !contains(module, "SOCKET") {
			s.reply(d, from, errReply("getPower", -32601, "Method not found"))
			return
		}
		s.reply(d, from, map[string]any{"method": "getPower", "env": "pro", "result": map[string]any{"power": pw}})
	case "registration":
		reg, _ := params["register"].(bool)
		if reg {
			s.mu.Lock()
			s.regs[mac]++
			s.lastReg[mac] = params
			d.pushTo = []*net.UDPAddr{from}
			s.mu.Unlock()
		}
		s.reply(d, from, map[string]any{"method": "registration", "env": "pro", "result": map[string]any{"mac": mac, "success": true}})
	case "setPilot":
		if code, msg := s.setPilot(d, params); code != 0 {
			s.reply(d, from, errReply("setPilot", code, msg))
			return
		}
		s.reply(d, from, map[string]any{"method": "setPilot", "env": "pro", "result": map[string]any{"success": true}})
		go s.push(d, "syncPilot")
	default:
		s.reply(d, from, errReply(m.Method, -32601, "Method not found"))
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func num(v any) (int, bool) {
	f, ok := v.(float64)
	return int(f), ok
}

func (s *Sim) setPilot(d *device, p map[string]any) (int, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := d.cfg.State
	if v, ok := p["state"].(bool); ok {
		st.On = v
	}
	if v, ok := num(p["dimming"]); ok {
		if v < 10 || v > 100 {
			return -32602, "Invalid params"
		}
		st.Dimming, st.On = v, true
	}
	_, hasR := p["r"]
	_, hasG := p["g"]
	_, hasB := p["b"]
	if hasR || hasG || hasB {
		if !contains(d.cfg.Module, "RGB") {
			return -32602, "Invalid params"
		}
		st.R, _ = num(p["r"])
		st.G, _ = num(p["g"])
		st.B, _ = num(p["b"])
		st.Temp, st.SceneID, st.On = 0, 0, true
	}
	if v, ok := num(p["temp"]); ok {
		if v < 1000 || v > 10000 || contains(d.cfg.Module, "DW") || contains(d.cfg.Module, "SOCKET") {
			return -32602, "Invalid params"
		}
		st.Temp, st.SceneID, st.On = v, 0, true
	}
	if v, ok := num(p["sceneId"]); ok {
		if v < 1 || contains(d.cfg.Module, "SOCKET") {
			return -32602, "Invalid params"
		}
		st.SceneID, st.Temp, st.On = v, 0, true
	}
	if v, ok := num(p["speed"]); ok {
		st.Speed = v
	}
	d.cfg.State = st
	s.sets[d.cfg.MAC] = append(s.sets[d.cfg.MAC], p)
	return 0, ""
}

// pilot returns the state as the device reports it.
func (s *Sim) pilot(d *device, src string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := d.cfg.State
	m := map[string]any{"mac": d.cfg.MAC, "rssi": -58, "src": src, "state": st.On}
	if contains(d.cfg.Module, "SOCKET") {
		return m
	}
	m["sceneId"] = st.SceneID
	m["dimming"] = st.Dimming
	switch {
	case st.SceneID != 0:
		m["speed"] = st.Speed
	case st.Temp != 0:
		m["temp"] = st.Temp
	case contains(d.cfg.Module, "RGB"):
		m["r"], m["g"], m["b"], m["c"], m["w"] = st.R, st.G, st.B, st.C, st.W
	}
	return m
}

func (s *Sim) push(d *device, method string) {
	s.mu.Lock()
	to := append([]*net.UDPAddr(nil), d.pushTo...)
	off := d.offline
	s.mu.Unlock()
	if off || len(to) == 0 {
		return
	}
	var params map[string]any
	if method == "firstBeat" {
		params = map[string]any{"mac": d.cfg.MAC, "homeId": 4242, "fwVersion": d.cfg.Firmware}
	} else {
		params = s.pilot(d, "udp")
	}
	b, _ := json.Marshal(map[string]any{"method": method, "env": "pro", "params": params})
	// A real light pushes from a port of its own, not its API port (38899):
	// a client must not take a push's source port as the device's.
	pc, err := net.ListenUDP("udp4", &net.UDPAddr{IP: d.conn.LocalAddr().(*net.UDPAddr).IP})
	if err != nil {
		return
	}
	for _, a := range to {
		_, _ = pc.WriteToUDP(b, a)
	}
	go func() {
		defer pc.Close()
		buf := make([]byte, 2048)
		_ = pc.SetReadDeadline(time.Now().Add(2 * time.Second))
		for {
			n, _, err := pc.ReadFromUDP(buf)
			if err != nil {
				return
			}
			var m struct {
				Method string          `json:"method"`
				Result json.RawMessage `json:"result"`
			}
			if json.Unmarshal(buf[:n], &m) == nil && m.Method == "syncPilot" && m.Result != nil {
				s.mu.Lock()
				s.acks++
				s.mu.Unlock()
			}
		}
	}()
	time.Sleep(time.Millisecond)
}
