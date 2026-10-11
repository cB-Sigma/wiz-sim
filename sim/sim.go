// Package sim simulates WiZ lights, plugs and ceiling fans on their local
// UDP API for tests and demos: each simulated device answers on its own
// UDP socket, a shared socket stands in for the broadcast address of
// discovery, and devices registered for pushes send syncPilot to the
// registering address when their state changes, from a port of their own
// as real lights do.
//
// The methods answered are getPilot, setPilot, setState, getSystemConfig,
// getModelConfig, getUserConfig, setUserConfig, getWifiConfig,
// setSystemConfig, setModelConfig, getPower, registration, reboot, reset,
// updateOta and pulse. Fan modules (FANDIM, DDTW) take fanState, fanMode,
// fanSpeed and fanRevrs; dual-head modules (DH) take ratio, or, with
// Device.PerHead, keep a state per head addressed with devices (the
// numbering of pywizlight 0.6.6). SetPushPort makes devices push to the
// registration's phoneIp as real devices do. Sockets are on
// 127.0.0.1 unless NewHost names another address. Built from the publicly
// described behaviour of the local API; not a WiZ product. Where public
// descriptions are silent (what reset keeps, what updateStatus values
// mean, what getWifiConfig holds), the simulator makes a choice of its own
// and says so on the method.
package sim

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// State is a simulated device's light (and fan) state.
type State struct {
	On      bool
	Dimming int // Device.MinDimming (10 unless set) to 100
	R, G, B int
	C, W    int
	Temp    int // kelvin; 0 in colour or scene mode
	SceneID int
	Speed   int
	// Fan modules: FanState 0 off, 1 on; FanMode 1 normal, 2 breeze;
	// FanSpeed 1 to Device.FanSpeeds; FanRevrs 0 forward, 1 reverse. The
	// fan is independent of the light (On).
	FanState, FanMode, FanSpeed, FanRevrs int
	// Ratio splits a dual-head module's light between its heads, 0 to 100.
	Ratio int
}

// Device configures a simulated device.
type Device struct {
	MAC      string // 12 lower-case hexadecimal digits
	Module   string // moduleName, such as ESP01_SHRGB1C_31
	Firmware string
	State    State
	// PowerMW is what getPower, and pc in the pilot, report (milliwatts);
	// plugs only.
	PowerMW int
	// FanSpeeds is the number of fan speeds a fan module reports (0: 6).
	FanSpeeds int
	// MinDimming is the lowest dimming setPilot accepts (0: 10). The
	// vendor's own library allows 1; 10 is what older descriptions give.
	MinDimming int
	// NoModelConfig makes the device answer getModelConfig with "method not
	// found", as firmware before 1.22 does; a fan module then reports its
	// speeds in getUserConfig.
	NoModelConfig bool
	// LatestFirmware is the version updateOta with fw "default" installs;
	// "" keeps the current one.
	LatestFirmware string
	// PerHead makes a dual-head module (DH in its name) keep a state per
	// head instead of splitting one light with ratio: State is head 1's,
	// Head2 head 2's (State is copied when Head2 is the zero State). The
	// numbering follows pywizlight 0.6.6: setPilot and setState take the
	// head, 1 or 2, in the devices key (without it both heads change);
	// getPilot takes devices 0 or 1 and answers with the head, 1 or 2, in
	// devices; syncPilot pushes carry the head the same way. A plain
	// getPilot answers with head 1's state, untagged. ratio is refused.
	PerHead bool
	Head2   State
}

type device struct {
	cfg        Device
	initial    Device
	conn       *net.UDPConn
	offline    bool
	restarting bool
	otaStatus  int
	pushTo     []*net.UDPAddr
	phoneIP    net.IP // the registration's phoneIp, when it is a usable IPv4 address
	user       map[string]any
	head2      State // head 2 of a PerHead device
}

// Sim is a set of simulated devices.
type Sim struct {
	mu         sync.Mutex
	devs       []*device
	bcast      *net.UDPConn
	calls      map[string]int
	sets       map[string][]map[string]any            // MAC → setPilot params
	reqs       map[string]map[string][]map[string]any // MAC → method → params
	regs       map[string]int                         // MAC → registrations with register true
	acks       int
	lastReg    map[string]map[string]any
	wg         sync.WaitGroup
	host       net.IP
	rebootTime time.Duration
	pushPort   int
	pulses     map[string]int
	// bcastAnswers: devices answer the broadcast socket's datagrams from it.
	bcastAnswers bool
	done         chan struct{}
	closeOnce    sync.Once
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
	return NewHostAt(host, 0)
}

// NewHostAt is NewHost with the broadcast socket on the given port (0 for
// a free one). Clients that always send discovery to port 38899, such as
// pywizlight, need it there.
func NewHostAt(host string, broadcastPort int) (*Sim, error) {
	ip := net.ParseIP(host).To4()
	if ip == nil {
		return nil, fmt.Errorf("sim: %q is not an IPv4 address", host)
	}
	s := &Sim{calls: map[string]int{}, sets: map[string][]map[string]any{}, reqs: map[string]map[string][]map[string]any{},
		regs: map[string]int{}, lastReg: map[string]map[string]any{}, pulses: map[string]int{}, host: ip, rebootTime: 2 * time.Second, done: make(chan struct{})}
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: ip, Port: broadcastPort})
	if err != nil {
		return nil, err
	}
	s.bcast = c
	s.wg.Add(1)
	go s.serveBroadcast()
	return s, nil
}

// Demo lists the devices of NewDemo: a colour bulb, a tunable-white bulb,
// a dimmable-white bulb and a plug.
func Demo() []Device {
	return []Device{
		{MAC: "a8bb50e4c101", Module: "ESP01_SHRGB1C_31", Firmware: "1.28.0", LatestFirmware: "1.32.0", State: State{On: true, Dimming: 80, R: 255, G: 120, B: 0}},
		{MAC: "a8bb50e4c102", Module: "ESP56_SHTW3_01", Firmware: "1.25.0", State: State{On: true, Dimming: 60, Temp: 2700}},
		{MAC: "a8bb50e4c103", Module: "ESP05_SHDW9_01", Firmware: "1.22.0", State: State{On: false, Dimming: 100, SceneID: 14}},
		{MAC: "a8bb50e4c104", Module: "ESP10_SOCKET_06", Firmware: "1.26.1", State: State{On: true}, PowerMW: 61500},
	}
}

// ExtendedDemo lists the devices of NewExtendedDemo: Demo's, a ceiling
// fan with a dimmable light (six speeds, fan on, light off), a ceiling fan
// with a tunable-white light (five speeds) and a dual-head colour lamp.
func ExtendedDemo() []Device {
	return append(Demo(),
		Device{MAC: "a8bb50e4c105", Module: "ESP03_FANDIMS_31", Firmware: "1.31.0", State: State{Dimming: 100, FanState: 1, FanMode: 1, FanSpeed: 3}},
		Device{MAC: "a8bb50e4c106", Module: "ESP20_DDTW1_01", Firmware: "1.30.0", FanSpeeds: 5, State: State{On: true, Dimming: 70, Temp: 3000, FanMode: 1, FanSpeed: 2}},
		Device{MAC: "a8bb50e4c107", Module: "ESP20_DHRGB_01", Firmware: "1.21.40", State: State{On: true, Dimming: 80, Temp: 2700, Ratio: 50}},
	)
}

// NewDemo returns a simulator with a colour bulb, a tunable-white bulb, a
// dimmable-white bulb and a plug, the kind of set an Indian home buys as
// "Philips Smart Wi-Fi LED".
func NewDemo() *Sim {
	s := New()
	for _, d := range Demo() {
		s.Add(d)
	}
	return s
}

// NewExtendedDemo returns a simulator with the devices of ExtendedDemo:
// NewDemo's four, two ceiling fans and a dual-head lamp.
func NewExtendedDemo() *Sim {
	s := New()
	for _, d := range ExtendedDemo() {
		s.Add(d)
	}
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
	dv := &device{cfg: d, initial: d, conn: c, user: defaultUserConfig(d), head2: initialHead2(d)}
	s.mu.Lock()
	s.devs = append(s.devs, dv)
	s.mu.Unlock()
	s.wg.Add(1)
	go s.serve(dv)
	return c.LocalAddr().String(), nil
}

// SetRebootTime sets how long a device stays silent while it restarts
// after reboot or a firmware update (2 s by default).
func (s *Sim) SetRebootTime(d time.Duration) {
	s.mu.Lock()
	s.rebootTime = d
	s.mu.Unlock()
}

// SetPushPort makes devices push to the registration's phoneIp at port,
// as real devices do (port 38900, wiz.PushPort). With 0, the default,
// they push to the address the registration came from instead: a
// simulator convenience that lets tests register from any free port.
// A registration whose phoneIp is not an IPv4 address (or is 0.0.0.0)
// is pushed to at its source address either way.
func (s *Sim) SetPushPort(port int) {
	s.mu.Lock()
	s.pushPort = port
	s.mu.Unlock()
}

// HeadState returns head 1's or head 2's state of a PerHead device (head
// 1 is State).
func (s *Sim) HeadState(mac string, head int) State {
	d := s.find(mac)
	s.mu.Lock()
	defer s.mu.Unlock()
	if d == nil {
		return State{}
	}
	if head == 2 {
		return d.head2
	}
	return d.cfg.State
}

// SetHeadState changes one head (1 or 2) of a PerHead device, as the phone
// app would, and pushes that head's syncPilot to registered addresses.
func (s *Sim) SetHeadState(mac string, head int, f func(*State)) {
	d := s.find(mac)
	if d == nil || (head != 1 && head != 2) {
		return
	}
	s.mu.Lock()
	if head == 2 {
		f(&d.head2)
	} else {
		f(&d.cfg.State)
	}
	s.mu.Unlock()
	s.push(d, "syncPilot", s.headPilot(d, head, "udp"))
}

// Pulses counts the pulse requests a device accepted.
func (s *Sim) Pulses(mac string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pulses[mac]
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

// Close stops every socket and every restart in progress.
func (s *Sim) Close() {
	s.closeOnce.Do(func() { close(s.done) })
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
	perHead := d.cfg.PerHead
	s.mu.Unlock()
	if perHead { // head 1's state; its push carries the head
		s.push(d, "syncPilot", s.headPilot(d, 1, "udp"))
		return
	}
	s.push(d, "syncPilot", nil)
}

// SetPower sets what a plug reports to getPower (milliwatts).
func (s *Sim) SetPower(mac string, mw int) {
	if d := s.find(mac); d != nil {
		s.mu.Lock()
		d.cfg.PowerMW = mw
		s.mu.Unlock()
	}
}

// SetOffline makes a device ignore every datagram (powered off at the
// wall); false also brings back a device that left the network after
// reset.
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
		s.push(d, "firstBeat", nil)
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

// Requests returns the params of every request of a method a device
// received while online, in order, refused ones included (setSystemConfig
// and setModelConfig are only recorded, never applied).
func (s *Sim) Requests(mac, method string) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.reqs[mac][method]...)
}

// Registrations counts a device's push registrations.
func (s *Sim) Registrations(mac string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.regs[mac]
}

// Registered reports whether a device has an address to push to.
func (s *Sim) Registered(mac string) bool {
	d := s.find(mac)
	s.mu.Lock()
	defer s.mu.Unlock()
	return d != nil && len(d.pushTo) > 0
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
		var via *net.UDPConn
		if s.bcastAnswers {
			via = s.bcast
		}
		s.mu.Unlock()
		for _, d := range devs {
			s.handle(d, buf[:n], from, via)
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
		s.handle(d, append([]byte(nil), buf[:n]...), from, nil)
	}
}

// replyVia answers from the device's socket, or from via when it is not
// nil.
func (s *Sim) replyVia(via *net.UDPConn, d *device, to *net.UDPAddr, v any) {
	b, _ := json.Marshal(v)
	if via == nil {
		via = d.conn
	}
	_, _ = via.WriteToUDP(b, to)
}

// SetBroadcastAnswers makes devices answer datagrams sent to the
// broadcast socket from that socket rather than from their own. Real
// devices answer discovery from port 38899, the port it was sent to; in
// the simulator every device has a port of its own, and a client behind
// NAT (a Docker container reaching the simulator through
// host.docker.internal, for example) only receives answers from the port
// it sent to. The answers then all come from the broadcast address, so
// the client learns each device's MAC but not its port: use Addr for that.
func (s *Sim) SetBroadcastAnswers(on bool) {
	s.mu.Lock()
	s.bcastAnswers = on
	s.mu.Unlock()
}

func errReply(method string, code int, msg string) map[string]any {
	return map[string]any{"method": method, "env": "pro", "error": map[string]any{"code": code, "message": msg}}
}

func okReply(method string, result any) map[string]any {
	return map[string]any{"method": method, "env": "pro", "result": result}
}

const (
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

func (s *Sim) handle(d *device, b []byte, from *net.UDPAddr, via *net.UDPConn) {
	var m struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(b, &m) != nil {
		return
	}
	s.mu.Lock()
	off := d.offline || d.restarting
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
	cfg := d.cfg
	mac, module, fw, pw := cfg.MAC, cfg.Module, cfg.Firmware, cfg.PowerMW
	if s.reqs[mac] == nil {
		s.reqs[mac] = map[string][]map[string]any{}
	}
	s.reqs[mac][m.Method] = append(s.reqs[mac][m.Method], params)
	ota := d.otaStatus
	s.mu.Unlock()
	switch m.Method {
	case "getPilot":
		if _, sel := params["devices"]; sel {
			// pywizlight 0.6.6's selector: 0 or 1 for head 1 or 2. Devices
			// without a state per head ignore it, as pywizlight expects of
			// such firmware.
			if !cfg.PerHead {
				s.replyVia(via, d, from, okReply("getPilot", s.pilot(d, "")))
				return
			}
			n, _, bad := inRange(params, "devices", 0, 1)
			if bad {
				s.replyVia(via, d, from, errReply("getPilot", codeInvalidParams, "Invalid params"))
				return
			}
			s.replyVia(via, d, from, okReply("getPilot", s.headPilot(d, n+1, "")))
			return
		}
		s.replyVia(via, d, from, okReply("getPilot", s.pilot(d, "")))
	case "pulse":
		// pywizlight's README describes delta (-100 to 100) and duration
		// (milliseconds). The simulator checks them and counts the pulse;
		// the light's state does not change.
		delta, _, okDelta := intParam(params, "delta")
		dur, _, okDur := intParam(params, "duration")
		if !okDelta || delta == 0 || delta < -100 || delta > 100 || !okDur || dur < 1 || kind(module) == kindSocket {
			s.replyVia(via, d, from, errReply("pulse", codeInvalidParams, "Invalid params"))
			return
		}
		s.mu.Lock()
		s.pulses[mac]++
		s.mu.Unlock()
		s.replyVia(via, d, from, okReply("pulse", map[string]any{"success": true}))
	case "getSystemConfig":
		s.replyVia(via, d, from, okReply("getSystemConfig", map[string]any{
			"mac": mac, "homeId": 4242, "roomId": 7, "moduleName": module, "fwVersion": fw, "groupId": 0, "typeId": 0, "ping": 0,
			"fwOtaStatus": ota,
		}))
	case "getModelConfig":
		if cfg.NoModelConfig {
			s.replyVia(via, d, from, errReply("getModelConfig", codeMethodNotFound, "Method not found"))
			return
		}
		s.replyVia(via, d, from, okReply("getModelConfig", modelConfig(cfg)))
	case "getUserConfig":
		s.mu.Lock()
		u := make(map[string]any, len(d.user))
		for k, v := range d.user {
			u[k] = v
		}
		s.mu.Unlock()
		s.replyVia(via, d, from, okReply("getUserConfig", u))
	case "setUserConfig":
		if msg := s.setUserConfig(d, params); msg != "" {
			s.replyVia(via, d, from, errReply("setUserConfig", codeInvalidParams, msg))
			return
		}
		s.replyVia(via, d, from, okReply("setUserConfig", map[string]any{"success": true}))
	case "getWifiConfig":
		// What real devices hold here is not publicly described; an opaque
		// string stands in for it.
		s.replyVia(via, d, from, okReply("getWifiConfig", fmt.Sprintf("%x", "wizsim "+mac)))
	case "setSystemConfig", "setModelConfig":
		// Factory settings: recorded (Requests), never applied.
		if len(params) == 0 {
			s.replyVia(via, d, from, errReply(m.Method, codeInvalidParams, "Invalid params"))
			return
		}
		s.replyVia(via, d, from, okReply(m.Method, map[string]any{"success": true}))
	case "getPower":
		if kind(module) != kindSocket {
			s.replyVia(via, d, from, errReply("getPower", codeMethodNotFound, "Method not found"))
			return
		}
		s.replyVia(via, d, from, okReply("getPower", map[string]any{"power": pw}))
	case "registration":
		reg, _ := params["register"].(bool)
		if reg {
			s.mu.Lock()
			s.regs[mac]++
			s.lastReg[mac] = params
			d.pushTo = []*net.UDPAddr{from}
			d.phoneIP = nil
			if str, ok := params["phoneIp"].(string); ok {
				if ip := net.ParseIP(str).To4(); ip != nil && !ip.IsUnspecified() {
					d.phoneIP = ip
				}
			}
			s.mu.Unlock()
		}
		s.replyVia(via, d, from, okReply("registration", map[string]any{"mac": mac, "success": true}))
	case "setPilot", "setState":
		heads, msg := s.setPilot(d, params, m.Method == "setPilot")
		if msg != "" {
			s.replyVia(via, d, from, errReply(m.Method, codeInvalidParams, msg))
			return
		}
		s.replyVia(via, d, from, okReply(m.Method, map[string]any{"success": true}))
		if len(heads) == 0 {
			go s.push(d, "syncPilot", nil)
			return
		}
		go func() {
			for _, h := range heads {
				s.push(d, "syncPilot", s.headPilot(d, h, "udp"))
			}
		}()
	case "reboot":
		s.replyVia(via, d, from, okReply("reboot", map[string]any{"success": true}))
		s.background(func() { s.restart(d, nil) })
	case "reset":
		// What a factory reset keeps is not publicly described. Here the
		// state and user configuration go back to the device's initial
		// ones, its push registration is dropped and it leaves the network
		// (as a device without Wi-Fi settings would) until SetOffline(mac,
		// false) sets it up again. The firmware version is kept.
		s.replyVia(via, d, from, okReply("reset", map[string]any{"success": true}))
		s.mu.Lock()
		d.cfg.State = d.initial.State
		d.head2 = initialHead2(d.initial)
		d.cfg.PowerMW = d.initial.PowerMW
		d.user = defaultUserConfig(d.cfg)
		d.pushTo = nil
		d.offline = true
		s.mu.Unlock()
	case "updateOta":
		target, msg := s.otaTarget(d, params)
		if msg != "" {
			s.replyVia(via, d, from, errReply("updateOta", codeInvalidParams, msg))
			return
		}
		s.replyVia(via, d, from, okReply("updateOta", map[string]any{"success": true}))
		s.background(func() { s.update(d, target) })
	default:
		s.replyVia(via, d, from, errReply(m.Method, codeMethodNotFound, "Method not found"))
	}
}

// The module kinds, told from the module name's second segment in the
// order public clients check it.
const (
	kindRGB     = "rgb"
	kindFanTW   = "fantw"
	kindTW      = "tw"
	kindSocket  = "socket"
	kindFanDim  = "fandim"
	kindDW      = "dw"
	kindUnknown = ""
)

func segment(module string) string {
	parts := strings.Split(strings.ToUpper(module), "_")
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

func kind(module string) string {
	seg := segment(module)
	switch {
	case strings.Contains(seg, "RGB"):
		return kindRGB
	case strings.Contains(seg, "DDTW"):
		return kindFanTW
	case strings.Contains(seg, "TW"):
		return kindTW
	case strings.Contains(seg, "SOCKET"):
		return kindSocket
	case strings.Contains(seg, "FANDIM"):
		return kindFanDim
	case strings.Contains(seg, "DW"):
		return kindDW
	}
	return kindUnknown
}

func isFan(k string) bool { return k == kindFanDim || k == kindFanTW }

func dualHead(module string) bool { return strings.Contains(segment(module), "DH") }

func fanSpeeds(d Device) int {
	if d.FanSpeeds > 0 {
		return d.FanSpeeds
	}
	return 6
}

// modelConfig is the simulator's getModelConfig answer: values of its own
// choosing in the publicly described keys.
func modelConfig(d Device) map[string]any {
	k := kind(d.Module)
	cct, nowc := []int{2700, 2700, 2700, 2700}, 1
	switch k {
	case kindRGB:
		cct, nowc = []int{2200, 2700, 6500, 6500}, 2
	case kindTW, kindFanTW:
		cct, nowc = []int{2700, 2700, 6500, 6500}, 2
	}
	fan := 0
	if isFan(k) {
		fan = fanSpeeds(d)
	}
	return map[string]any{"ps": 1, "pwmFreq": 1000, "pwmRange": []int{0, 100}, "wcr": 30, "nowc": nowc, "cctRange": cct,
		"renderFactor": []int{255, 0, 255, 255, 0, 0, 0, 0, 0, 0}, "hasAdjMinDim": 0, "hasTapSensor": 0, "pm": 0, "fanSpeed": fan}
}

// defaultUserConfig is a device's getUserConfig answer before any
// setUserConfig, with values of the simulator's own choosing.
func defaultUserConfig(d Device) map[string]any {
	ext := []int{2700, 6500}
	if kind(d.Module) == kindRGB {
		ext = []int{2200, 6500}
	}
	u := map[string]any{"fadeIn": 450, "fadeOut": 500, "fadeNight": false, "dftDim": 100, "pwmRange": []int{0, 100},
		"whiteRange": []int{2700, 6500}, "extRange": ext, "opMode": 0, "po": false}
	if d.NoModelConfig && isFan(kind(d.Module)) {
		u["fanSpeed"] = fanSpeeds(d)
	}
	return u
}

// intParam reads an integer param: present tells whether the key is
// there, ok whether its value is a whole number.
func intParam(p map[string]any, key string) (n int, present, ok bool) {
	v, present := p[key]
	if !present {
		return 0, false, false
	}
	f, isNum := v.(float64)
	if !isNum || f != float64(int(f)) {
		return 0, true, false
	}
	return int(f), true, true
}

// inRange checks an optional integer param; bad is true when it is there
// but not a whole number in lo..hi.
func inRange(p map[string]any, key string, lo, hi int) (n int, present, bad bool) {
	n, present, ok := intParam(p, key)
	if present && (!ok || n < lo || n > hi) {
		return 0, true, true
	}
	return n, present, false
}

// initialHead2 is head 2's initial state of a PerHead device: Head2, or a
// copy of State when Head2 is the zero State.
func initialHead2(d Device) State {
	if !d.PerHead || d.Head2 != (State{}) {
		return d.Head2
	}
	return d.State
}

// setPilot applies setPilot (on true) or setState (on false: the light is
// not switched on unless state says so). It returns the heads changed on a
// PerHead device (nil otherwise) and a refusal message, "" when applied.
func (s *Sim) setPilot(d *device, p map[string]any, on bool) ([]int, string) {
	const invalid = "Invalid params"
	s.mu.Lock()
	defer s.mu.Unlock()
	if !d.cfg.PerHead {
		if _, ok := p["devices"]; ok {
			return nil, invalid
		}
		st, msg := s.applyPilot(d, d.cfg.State, p, on)
		if msg != "" {
			return nil, msg
		}
		d.cfg.State = st
		s.sets[d.cfg.MAC] = append(s.sets[d.cfg.MAC], p)
		return nil, ""
	}
	if _, ok := p["ratio"]; ok {
		return nil, invalid // a lamp with a state per head does not split one light
	}
	heads := []int{1, 2}
	if _, ok := p["devices"]; ok {
		n, _, bad := inRange(p, "devices", 1, 2)
		if bad {
			return nil, invalid
		}
		heads = []int{n}
	}
	st1, st2 := d.cfg.State, d.head2
	for _, h := range heads {
		cur := &st1
		if h == 2 {
			cur = &st2
		}
		next, msg := s.applyPilot(d, *cur, p, on)
		if msg != "" {
			return nil, msg
		}
		*cur = next
	}
	d.cfg.State, d.head2 = st1, st2
	s.sets[d.cfg.MAC] = append(s.sets[d.cfg.MAC], p)
	return heads, ""
}

// applyPilot applies setPilot's params to st and returns the new state,
// or a refusal message. The caller holds s.mu.
func (s *Sim) applyPilot(d *device, st State, p map[string]any, on bool) (State, string) {
	const invalid = "Invalid params"
	k := kind(d.cfg.Module)
	light := func() {
		if on {
			st.On = true
		}
	}
	if v, ok := p["state"]; ok {
		b, isBool := v.(bool)
		if !isBool {
			return st, invalid
		}
		st.On = b
	}
	minDim := d.cfg.MinDimming
	if minDim <= 0 {
		minDim = 10
	}
	if v, present, bad := inRange(p, "dimming", minDim, 100); bad {
		return st, invalid
	} else if present {
		st.Dimming = v
		light()
	}
	_, hasR := p["r"]
	_, hasG := p["g"]
	_, hasB := p["b"]
	if hasR || hasG || hasB {
		if k != kindRGB {
			return st, invalid
		}
		var rgb [3]int
		for i, key := range []string{"r", "g", "b"} {
			v, _, bad := inRange(p, key, 0, 255)
			if bad {
				return st, invalid
			}
			rgb[i] = v
		}
		st.R, st.G, st.B = rgb[0], rgb[1], rgb[2]
		st.Temp, st.SceneID = 0, 0
		light()
	}
	for key, dst := range map[string]*int{"c": &st.C, "w": &st.W} {
		if v, present, bad := inRange(p, key, 0, 255); bad || present && k != kindRGB {
			return st, invalid
		} else if present {
			*dst = v
		}
	}
	if v, present, bad := inRange(p, "temp", 1000, 10000); bad {
		return st, invalid
	} else if present {
		if k == kindDW || k == kindSocket || k == kindFanDim {
			return st, invalid
		}
		st.Temp, st.SceneID = v, 0
		light()
	}
	if v, present, bad := inRange(p, "sceneId", 1, 1<<31-1); bad {
		return st, invalid
	} else if present {
		if k == kindSocket {
			return st, invalid
		}
		st.SceneID, st.Temp = v, 0
		light()
	}
	if v, present, bad := inRange(p, "speed", 10, 200); bad {
		return st, invalid
	} else if present {
		st.Speed = v
	}
	for _, f := range []struct {
		key    string
		lo, hi int
		dst    *int
	}{{"fanState", 0, 1, &st.FanState}, {"fanMode", 1, 2, &st.FanMode}, {"fanSpeed", 1, fanSpeeds(d.cfg), &st.FanSpeed}, {"fanRevrs", 0, 1, &st.FanRevrs}} {
		v, present, bad := inRange(p, f.key, f.lo, f.hi)
		if bad || present && !isFan(k) {
			return st, invalid
		}
		if present {
			*f.dst = v
		}
	}
	if v, present, bad := inRange(p, "ratio", 0, 100); bad || present && !dualHead(d.cfg.Module) {
		return st, invalid
	} else if present {
		st.Ratio = v
	}
	return st, ""
}

// setUserConfig stores the setUserConfig params the vendor's own library
// describes. Any other key, and an empty request, are refused: the
// simulator's choice, to catch typing errors in clients.
func (s *Sim) setUserConfig(d *device, p map[string]any) string {
	if len(p) == 0 {
		return "Invalid params"
	}
	set := map[string]any{}
	for key, v := range p {
		switch key {
		case "whiteRange", "extRange", "pwmRange":
			l, ok := v.([]any)
			if !ok || len(l) != 2 {
				return "Invalid params"
			}
			var r [2]int
			for i, x := range l {
				f, ok := x.(float64)
				if !ok || f != float64(int(f)) || f < 0 {
					return "Invalid params"
				}
				r[i] = int(f)
			}
			if r[1] < r[0] {
				return "Invalid params"
			}
			set[key] = []int{r[0], r[1]}
		case "dftDim":
			n, _, bad := inRange(p, key, 1, 100)
			if bad {
				return "Invalid params"
			}
			set[key] = n
		case "userConfigTs":
			n, _, ok := intParam(p, key)
			if !ok {
				return "Invalid params"
			}
			set[key] = n
		case "startupMode":
			str, ok := v.(string)
			if !ok {
				return "Invalid params"
			}
			set[key] = str
		default:
			return "Invalid params"
		}
	}
	s.mu.Lock()
	for k, v := range set {
		d.user[k] = v
	}
	s.mu.Unlock()
	return ""
}

// otaTarget checks updateOta params (fw a version or "default"; force 0
// or 1 when given) and returns the version to install.
func (s *Sim) otaTarget(d *device, p map[string]any) (string, string) {
	fw, ok := p["fw"].(string)
	if !ok || fw == "" {
		return "", "Invalid params"
	}
	if _, _, bad := inRange(p, "force", 0, 1); bad {
		return "", "Invalid params"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if fw == "default" {
		if d.cfg.LatestFirmware == "" {
			return d.cfg.Firmware, ""
		}
		return d.cfg.LatestFirmware, ""
	}
	return fw, ""
}

// background runs f counted in the wait group, so Close waits for it.
func (s *Sim) background(f func()) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		f()
	}()
}

// sleep waits d; false when the simulator closed meanwhile.
func (s *Sim) sleep(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-s.done:
		return false
	}
}

// restart makes a device silent for the reboot time, runs then (under the
// lock) and announces the device with a firstBeat. The simulator keeps
// push registrations across a restart and sends firstBeat to them; the
// vendor's own library describes firstBeat as broadcast.
func (s *Sim) restart(d *device, then func()) {
	s.mu.Lock()
	d.restarting = true
	wait := s.rebootTime
	s.mu.Unlock()
	if !s.sleep(wait) {
		return
	}
	s.mu.Lock()
	if then != nil {
		then()
	}
	d.restarting = false
	s.mu.Unlock()
	s.push(d, "firstBeat", nil)
}

// update simulates a firmware update: updateOta progress pushes with
// updateStatus 1, 2 and 3 (stand-in values: their meaning on real devices
// is not publicly described; fwOtaStatus follows them), then a restart
// into the target version.
func (s *Sim) update(d *device, target string) {
	s.mu.Lock()
	step := s.rebootTime / 4
	s.mu.Unlock()
	for status := 1; status <= 3; status++ {
		s.mu.Lock()
		d.otaStatus = status
		s.mu.Unlock()
		s.push(d, "updateOta", map[string]any{"updateStatus": status})
		if !s.sleep(step) {
			return
		}
	}
	s.restart(d, func() {
		d.cfg.Firmware = target
		d.otaStatus = 0
	})
}

// pilot returns the state as the device reports it.
func (s *Sim) pilot(d *device, src string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return pilotOf(d, d.cfg.State, src)
}

// headPilot returns one head's state of a PerHead device, tagged with the
// head in devices.
func (s *Sim) headPilot(d *device, head int, src string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := d.cfg.State
	if head == 2 {
		st = d.head2
	}
	m := pilotOf(d, st, src)
	m["devices"] = head
	return m
}

// pilotOf returns st as the device reports it. The caller holds s.mu.
func pilotOf(d *device, st State, src string) map[string]any {
	k := kind(d.cfg.Module)
	m := map[string]any{"mac": d.cfg.MAC, "rssi": -58, "src": src, "state": st.On}
	if k == kindSocket {
		m["pc"] = d.cfg.PowerMW
		return m
	}
	m["sceneId"] = st.SceneID
	m["dimming"] = st.Dimming
	switch {
	case st.SceneID != 0:
		m["speed"] = st.Speed
	case st.Temp != 0:
		m["temp"] = st.Temp
	case k == kindRGB:
		m["r"], m["g"], m["b"], m["c"], m["w"] = st.R, st.G, st.B, st.C, st.W
	}
	if isFan(k) {
		m["fanState"], m["fanMode"], m["fanSpeed"], m["fanRevrs"] = st.FanState, st.FanMode, st.FanSpeed, st.FanRevrs
	}
	if dualHead(d.cfg.Module) && !d.cfg.PerHead {
		m["ratio"] = st.Ratio
	}
	return m
}

// push sends a push to the device's registered addresses: a syncPilot
// with the pilot (or with params when given), a firstBeat, or method with
// params.
func (s *Sim) push(d *device, method string, params map[string]any) {
	s.mu.Lock()
	to := append([]*net.UDPAddr(nil), d.pushTo...)
	if s.pushPort > 0 && d.phoneIP != nil && len(to) > 0 {
		// Real devices push to the registration's phoneIp, port 38900.
		to = []*net.UDPAddr{{IP: d.phoneIP, Port: s.pushPort}}
	}
	off := d.offline
	mac, fw := d.cfg.MAC, d.cfg.Firmware
	s.mu.Unlock()
	if off || len(to) == 0 {
		return
	}
	switch method {
	case "firstBeat":
		params = map[string]any{"mac": mac, "homeId": 4242, "fwVersion": fw}
	case "syncPilot":
		if params == nil {
			params = s.pilot(d, "udp")
		}
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
