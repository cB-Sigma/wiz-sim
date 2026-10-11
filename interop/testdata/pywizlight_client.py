# Copyright 2026 Abhinav Gautam
# SPDX-License-Identifier: Apache-2.0
#
# Drives pywizlight's public classes against the simulated devices of
# github.com/cB-Sigma/wiz-sim. Our own script; it contains no pywizlight
# code. Each check is printed as one JSON line {"check", "ok", "detail"};
# a line {"need": ..., "args": ...} asks the Go test to act (or to check
# what the simulator received) and the script waits for "go" on stdin.

import argparse
import asyncio
import json
import socket
import sys
import traceback

from pywizlight import PilotBuilder, wizlight
from pywizlight._version import __version__ as pywizlight_version
from pywizlight.bulblibrary import BulbClass
from pywizlight.discovery import find_wizlights
from pywizlight.exceptions import WizLightConnectionError, WizLightMethodNotFound
from pywizlight.push_manager import PushManager


def out(obj):
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()


def check(name, ok, detail=""):
    out({"check": name, "ok": bool(ok), "detail": str(detail)})


async def need(what, **args):
    out({"need": what, "args": args})
    line = await asyncio.get_event_loop().run_in_executor(None, sys.stdin.readline)
    return line.strip()


async def expect_refused(name, coro):
    try:
        await coro
    except WizLightConnectionError as ex:
        check(name, True, f"refused: {ex}")
        return
    check(name, False, "accepted")


async def wait_for(pred, seconds=5.0):
    loop = asyncio.get_event_loop()
    end = loop.time() + seconds
    while loop.time() < end:
        if pred():
            return True
        await asyncio.sleep(0.05)
    return pred()


async def run(a):
    check("pywizlight", True, pywizlight_version)
    devices = json.loads(a.devices)
    by_kind = {d["kind"]: d for d in devices}
    ip = socket.gethostbyname(a.host)

    # Discovery: pywizlight sends to port 38899 of the address given.
    found = await find_wizlights(wait_time=2.5, broadcast_address=ip)
    macs = sorted(b.mac_address for b in found)
    want = sorted(d["mac"] for d in devices)
    check("discovery", macs == want, f"found {macs}")

    lights = {}
    for d in devices:
        lights[d["kind"]] = wizlight(ip, port=d["port"])

    # Bulb type from getSystemConfig, getModelConfig and getUserConfig.
    expect_types = {
        "rgb": (BulbClass.RGB, dict(color=True, color_tmp=True, effect=True, dual_head=False)),
        "tw": (BulbClass.TW, dict(color=False, color_tmp=True, effect=True)),
        "dw": (BulbClass.DW, dict(color=False, color_tmp=False, effect=True)),
        "socket": (BulbClass.SOCKET, dict(brightness=False)),
        "fandim": (BulbClass.FANDIM, dict(fan=True, fan_breeze_mode=True, fan_reverse=True)),
        "fantw": (BulbClass.FANTW, dict(fan=True, color_tmp=True)),
        "dh": (BulbClass.RGB, dict(dual_head=True)),
        "perhead": (BulbClass.RGB, dict(dual_head=True)),
    }
    for kind, light in lights.items():
        try:
            bt = await light.get_bulbtype()
            cls, feats = expect_types[kind]
            bad = [k for k, v in feats.items() if getattr(bt.features, k) != v]
            mac = await light.getMac()
            check(f"bulbtype-{kind}", bt.bulb_type == cls and not bad and mac == by_kind[kind]["mac"],
                  f"{bt.as_dict()} mac {mac} mismatched {bad}")
        except Exception as ex:  # noqa: BLE001
            check(f"bulbtype-{kind}", False, f"{type(ex).__name__}: {ex}")

    rgb = lights["rgb"]
    bt = await rgb.get_bulbtype()
    check("kelvin-range-rgb", bt.kelvin_range is not None and bt.kelvin_range.min == 2200 and bt.kelvin_range.max == 6500,
          bt.kelvin_range)
    check("fan-speed-range", lights["fandim"].fanSpeedRange == 6 and lights["fantw"].fanSpeedRange == 5,
          f"{lights['fandim'].fanSpeedRange} {lights['fantw'].fanSpeedRange}")
    scenes = await lights["dw"].getSupportedScenes()
    check("supported-scenes-dw", "Night light" in scenes and "Ocean" not in scenes, scenes)

    # Colour, brightness, colour temperature, scenes, speed, on and off.
    await rgb.turn_on(PilotBuilder(rgb=(255, 0, 0), brightness=128))
    st = (await rgb.updateState())[0]
    check("rgb", st.get_state() is True and st.get_rgb() == (255, 0, 0) and st.get_brightness() == 128,
          st.pilotResult)
    for hue, sat in [(0, 100), (30, 100), (60, 100), (200, 80), (300, 40), (90, 0)]:
        await rgb.turn_on(PilotBuilder(hucolor=(hue, sat)))
        await need("check-hs", hue=hue, sat=sat)
    for r, g, b in [(255, 128, 0), (10, 200, 90), (255, 255, 255)]:
        await rgb.turn_on(PilotBuilder(rgb=(r, g, b)))
        await need("check-rgb", r=r, g=g, b=b)
    await rgb.turn_on(PilotBuilder(rgbww=(1, 2, 3, 4, 5)))
    st = (await rgb.updateState())[0]
    check("rgbww", st.get_rgbww() == (1, 2, 3, 4, 5), st.pilotResult)
    await rgb.turn_on(PilotBuilder(colortemp=4000, brightness=255))
    st = (await rgb.updateState())[0]
    check("colortemp", st.get_colortemp() == 4000 and st.get_brightness() == 255, st.pilotResult)
    await rgb.turn_on(PilotBuilder(scene=4, speed=150))
    st = (await rgb.updateState())[0]
    check("scene-speed", st.get_scene_id() == 4 and st.get_scene() == "Party" and st.get_speed() == 150, st.pilotResult)
    await rgb.set_speed(60)
    st = (await rgb.updateState())[0]
    check("set-speed", st.get_speed() == 60, st.pilotResult)
    await rgb.turn_off()
    st = (await rgb.updateState())[0]
    check("turn-off", st.get_state() is False, st.pilotResult)
    await rgb.set_state(PilotBuilder(brightness=64))
    st = (await rgb.updateState())[0]
    check("set-state-keeps-off", st.get_state() is False and st.get_brightness() == 64, st.pilotResult)
    await rgb.lightSwitch()
    st = (await rgb.updateState())[0]
    check("light-switch", st.get_state() is True, st.pilotResult)

    tw = lights["tw"]
    await tw.turn_on(PilotBuilder(colortemp=2700, brightness=200))
    st = (await tw.updateState())[0]
    check("tw-colortemp", st.get_colortemp() == 2700, st.pilotResult)
    await expect_refused("tw-refuses-rgb", tw.turn_on(PilotBuilder(rgb=(0, 0, 255))))
    dw = lights["dw"]
    await dw.turn_on(PilotBuilder(brightness=51))
    st = (await dw.updateState())[0]
    check("dw-brightness", st.get_state() is True and st.get_brightness() == 51, st.pilotResult)
    await expect_refused("dw-refuses-colortemp", dw.turn_on(PilotBuilder(colortemp=3000)))

    # Power: getPower on the plug, pc in its pilot, none on a bulb.
    plug = lights["socket"]
    watts = await plug.get_power()
    check("plug-get-power", watts == 61.5, watts)
    st = (await plug.updateState())[0]
    check("plug-pilot-power", st.get_power() == 61.5, st.pilotResult)
    watts = await lights["tw"].get_power()
    check("bulb-no-power", watts is None and lights["tw"].power_monitoring is False, watts)
    try:
        await lights["tw"].send({"method": "getPower"})
        check("method-not-found", False, "answered")
    except WizLightMethodNotFound:
        check("method-not-found", True, "WizLightMethodNotFound")

    # Fans.
    fan = lights["fandim"]
    await fan.fan_turn_on(mode=1, speed=3)
    st = (await fan.updateState())[0]
    check("fan-on", st.get_fan_state() == 1 and st.get_fan_mode() == 1 and st.get_fan_speed() == 3, st.pilotResult)
    await fan.fan_set_state(mode=2, reverse=1)
    st = (await fan.updateState())[0]
    check("fan-breeze-reverse", st.get_fan_mode() == 2 and st.get_fan_reverse() == 1, st.pilotResult)
    await fan.fanSwitch()
    st = (await fan.updateState())[0]
    check("fan-switch-off", st.get_fan_state() == 0, st.pilotResult)
    try:
        await fan.fan_set_state(speed=7)
        check("fan-speed-limit", False, "speed 7 sent")
    except ValueError as ex:
        check("fan-speed-limit", True, f"pywizlight refused before sending: {ex}")
    fantw = lights["fantw"]
    await fantw.fan_turn_on(speed=5)
    await fantw.turn_on(PilotBuilder(colortemp=5000))
    st = (await fantw.updateState())[0]
    check("fantw", st.get_fan_speed() == 5 and st.get_colortemp() == 5000, st.pilotResult)

    # Dual head: ratio lamp and a lamp with a state per head.
    dh = lights["dh"]
    await dh.set_ratio(30)
    states = await dh.updateState()
    check("ratio", len(states) == 1 and states[0].get_ratio() == 30, [s.pilotResult if s else None for s in states])
    ph = lights["perhead"]
    states = await ph.updateState()
    check("per-head-poll", len(states) == 2 and states[0] and states[1]
          and states[0].pilotResult.get("devices") == 1 and states[1].pilotResult.get("devices") == 2
          and states[0].pilotResult.get("dimming") == 25 and states[1].pilotResult.get("dimming") == 75,
          [s.pilotResult if s else None for s in states])
    await ph.turn_on(PilotBuilder(brightness=128), device=2)
    await need("check-head", head=2, on=True, dimming=50)
    await ph.turn_off(device=1)
    await need("check-head", head=1, on=False, dimming=25)

    # Pushes: pywizlight listens on port 38900; the simulator pushes to the
    # registration's phoneIp at the port the test publishes to it.
    pm = PushManager().get()
    started = await pm.start(ip)
    if started and a.push_ip:
        pm.register_msg["params"]["phoneIp"] = a.push_ip
    pushed = []
    ok = await rgb.start_push(lambda states: pushed.append(states[0].pilotResult if states and states[0] else None))
    check("push-start", ok, pm.diagnostics)
    await need("wait-registered", mac=by_kind["rgb"]["mac"])
    await need("change", mac=by_kind["rgb"]["mac"], dimming=42)
    got = await wait_for(lambda: any(p and p.get("dimming") == 42 for p in pushed))
    check("push-syncpilot", got and rgb.state[0].get_brightness() == 107, pushed)

    seen = []
    rgb.set_discovery_callback(lambda b: seen.append(b.mac_address))
    await need("firstbeat", mac=by_kind["rgb"]["mac"])
    got = await wait_for(lambda: by_kind["rgb"]["mac"] in seen)
    check("push-firstbeat", got, seen)

    heads = []
    await ph.start_push(lambda states: heads.append([s.pilotResult if s else None for s in states]))
    await need("wait-registered", mac=by_kind["perhead"]["mac"])
    await need("change-head", mac=by_kind["perhead"]["mac"], head=2, dimming=90)
    got = await wait_for(lambda: len(ph.state) == 2 and ph.state[1] is not None
                         and ph.state[1].pilotResult.get("dimming") == 90)
    check("push-per-head", got and ph.state[0] is not None and ph.state[0].pilotResult.get("devices") == 1, heads)

    # Maintenance.
    await dw.reboot()
    check("reboot", True, "answered")

    for light in lights.values():
        await light.async_close()


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--host", required=True)
    p.add_argument("--devices", required=True)
    p.add_argument("--push-ip", default="")
    a = p.parse_args()
    try:
        asyncio.run(run(a))
    except Exception as ex:  # noqa: BLE001
        check("script", False, f"{type(ex).__name__}: {ex}\n{traceback.format_exc()}")


if __name__ == "__main__":
    main()
