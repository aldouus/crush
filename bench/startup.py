#!/usr/bin/env python3
"""Measure interactive Crush startup: exec -> first frame -> ready.

Usage: bench/startup.py <crush-binary> [runs] [--warm] [--json out.json]

Each run launches the TUI under a pty (`script`) in a fresh temp project
with HOME/XDG/CRUSH_GLOBAL_* isolated in a temp dir and a private server
socket (never the live daemon). The binary records phase timestamps via
CRUSH_STARTUP_TRACE. "ready" is when the first frame is drawn and the
initial workspace overviews have loaded. Cold runs spawn a fresh server
every time; --warm reuses one server across runs. The first run of
either mode is a discarded warmup.
Every process that wrote to the trace is killed afterwards.
"""

import json
import os
import shutil
import signal
import statistics
import subprocess
import sys
import tempfile
import time

SERVER_PHASES = ["server_listen", "ws_build", "ws_config", "ws_db",
                 "ws_skills", "ws_app", "ws_done"]
PHASES = ["main", "server_ready", "workspace_created", "agent_init",
          "program_start", "first_frame", "overviews_loaded"]


def read_trace(path):
    marks, pids = {}, set()
    try:
        with open(path) as f:
            for line in f:
                parts = line.split()
                if len(parts) != 3:
                    continue
                pid, phase, ns = int(parts[0]), parts[1], int(parts[2])
                pids.add(pid)
                marks.setdefault((pid, phase), ns)
    except FileNotFoundError:
        pass
    return marks, pids


def kill(pids):
    for pid in pids:
        try:
            os.kill(pid, signal.SIGKILL)
        except ProcessLookupError:
            pass


def run_once(binary, root, sock, trace, timeout=30.0):
    proj = tempfile.mkdtemp(dir=root, prefix="proj-")
    env = dict(os.environ)
    env.update({
        "HOME": f"{root}/home", "XDG_CONFIG_HOME": f"{root}/config",
        "XDG_DATA_HOME": f"{root}/data", "XDG_CACHE_HOME": f"{root}/cache",
        "XDG_STATE_HOME": f"{root}/state",
        "CRUSH_GLOBAL_CONFIG": f"{root}/config/crush",
        "CRUSH_GLOBAL_DATA": f"{root}/data/crush",
        "CRUSH_DISABLE_METRICS": "1", "DO_NOT_TRACK": "1",
        "CRUSH_STARTUP_TRACE": trace, "TERM": "xterm-256color",
        "COLUMNS": "120", "LINES": "40",
    })
    if os.path.exists(trace):
        os.unlink(trace)
    t0 = time.time_ns()
    proc = subprocess.Popen(
        ["script", "-q", "/dev/null", binary, "-H", f"unix://{sock}",
         "-c", proj],
        env=env, stdin=subprocess.PIPE, stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL, start_new_session=True)
    client = None
    result = None
    deadline = time.time() + timeout
    while time.time() < deadline:
        marks, _ = read_trace(trace)
        for (pid, phase) in marks:
            if phase == "program_start":
                client = pid
        if client is not None:
            ff = marks.get((client, "first_frame"))
            ov = marks.get((client, "overviews_loaded"))
            if ff and ov:
                result = {p: (marks[(client, p)] - t0) / 1e6
                          for p in PHASES if (client, p) in marks}
                result["ready"] = (max(ff, ov) - t0) / 1e6
                for (pid, phase), ns in marks.items():
                    if pid != client and phase in SERVER_PHASES:
                        result["srv." + phase] = (ns - t0) / 1e6
                break
        time.sleep(0.002)
    try:
        os.killpg(proc.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
    proc.wait()
    if client:
        kill([client])
    shutil.rmtree(proj, ignore_errors=True)
    return result


def main():
    args = sys.argv[1:]
    warm = "--warm" in args
    out = None
    if "--json" in args:
        out = args[args.index("--json") + 1]
    pos = [a for a in args if not a.startswith("--") and a != out]
    binary = os.path.abspath(pos[0])
    runs = int(pos[1]) if len(pos) > 1 else 15
    root = tempfile.mkdtemp(prefix="crush-startup-")
    for d in ("home", "config", "data", "cache", "state"):
        os.makedirs(f"{root}/{d}", exist_ok=True)
    sock = f"{root}/s.sock"
    trace = f"{root}/trace.txt"
    alltrace = f"{root}/all.txt"
    results = []
    servers = set()
    try:
        total = runs + 1
        for i in range(total):
            r = run_once(binary, root, sock, trace)
            _, pids = read_trace(trace)
            if os.path.exists(trace):
                with open(trace) as src, open(alltrace, "a") as dst:
                    dst.write(src.read())
            if not warm:
                kill(pids)
                time.sleep(0.05)
            else:
                servers |= pids
            if r is None:
                print(f"run {i}: timeout", file=sys.stderr)
                continue
            if i == 0:
                continue
            results.append(r)
    finally:
        _, pids = read_trace(alltrace)
        kill(pids | servers)
        shutil.rmtree(root, ignore_errors=True)
    if not results:
        sys.exit("no successful runs")
    keys = PHASES + ["ready"] + ["srv." + p for p in SERVER_PHASES]
    summary = {}
    print(f"{'phase':<20}{'median ms':>12}{'stddev':>10}  (n={len(results)}, {'warm' if warm else 'cold'})")
    for k in keys:
        vals = [r[k] for r in results if k in r]
        if not vals:
            continue
        med = statistics.median(vals)
        sd = statistics.stdev(vals) if len(vals) > 1 else 0.0
        summary[k] = {"median": med, "stddev": sd, "values": vals}
        print(f"{k:<20}{med:>12.1f}{sd:>10.1f}")
    if out:
        with open(out, "w") as f:
            json.dump(summary, f, indent=1)


if __name__ == "__main__":
    main()
