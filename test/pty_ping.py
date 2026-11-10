#!/usr/bin/env python3
"""Ping runs continuously and is stopped by Ctrl-C without leaving the CLI (real PTY, live daemon).
Usage: unshare -n -u -m python3 test/pty_ping.py [binary]"""
import os, pty, select, subprocess, sys, tempfile, time

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
BIN = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "dist", "necorouter")
root = tempfile.mkdtemp(prefix="nr.", dir="/tmp")
os.makedirs(root + "/etc/necorouter"); os.makedirs(root + "/fakebin")
open(root + "/etc/necorouter/startup-config", "w").write(
    "hostname lab-rtr\nend\n")
# iputils ping imitation: without -c it runs forever; on SIGINT it prints statistics
open(root + "/fakebin/ping", "w").write("""#!/bin/sh
echo "ARGS: $*"
trap 'echo "--- stats ---"; exit 0' INT
i=0; while :; do i=$((i+1)); echo "64 bytes icmp_seq=$i"; sleep 0.1; done
""")
os.chmod(root + "/fakebin/ping", 0o755)
env = dict(os.environ, NECOROUTER_ROOT=root, PATH=root + "/fakebin:" + os.environ["PATH"])
d = subprocess.Popen([BIN, "daemon"], env=env, stderr=open(root + "/d.log", "w"))
for _ in range(300):
    if os.path.exists(root + "/run/necorouter/ctl.sock") or d.poll() is not None: break
    time.sleep(0.1)
if not os.path.exists(root + "/run/necorouter/ctl.sock"):
    if d.poll() is None: os.kill(d.pid, 3)  # SIGQUIT: the Go runtime writes all goroutine stacks to d.log
    time.sleep(1)
    print("processes:\n" + subprocess.run(["ps", "-ef"], capture_output=True, text=True).stdout)
    sys.exit("necorouter daemon did not create the control socket (rc=%s); d.log:\n%s"
             % (d.poll(), open(root + "/d.log").read()))
pid, fd = pty.fork()
if pid == 0:
    os.execve(BIN, [BIN, "shell"], env)
def read(t):
    out = b""; end = time.time() + t
    while time.time() < end:
        r, _, _ = select.select([fd], [], [], 0.1)
        if r:
            try: c = os.read(fd, 4096)
            except OSError: break
            if not c: break
            out += c
    return out.decode(errors="replace")
def send(s, t=0.6): os.write(fd, s.encode()); return read(t)
fails = []
def check(n, ok, det=""):
    print(("PASS " if ok else "FAIL ") + n + ("" if ok else " -> " + det))
    if not ok: fails.append(n)
read(1.5); send("enable\r")
o = send("ping 10.0.0.1\r", 1.0)
check("ping started without -c", "ARGS: -W 2 10.0.0.1" in o and "-c" not in o, repr(o[:200]))
n1 = o.count("icmp_seq"); o2 = read(1.0)
check("ping keeps running (>5 packets)", n1 + o2.count("icmp_seq") > 5, repr(o2[-100:]))
o = send("\x03", 1.2)
check("Ctrl-C stops ping with statistics", "--- stats ---" in o, repr(o))
check("prompt returns, CLI still alive", "lab-rtr#" in o, repr(o))
time.sleep(0.5); tail = read(0.5)
check("no more packets after stop", "icmp_seq" not in tail, repr(tail))
o = send("show hostname\r")
check("CLI usable after ping", "lab-rtr" in o, repr(o))
o = send("ping 10.0.0.2\r", 0.8); o = send("\x03", 1.0)
check("second ping works and stops", "--- stats ---" in o and "lab-rtr#" in o, repr(o))
try: os.kill(pid, 9)
except Exception: pass
d.terminate(); d.wait()
print("FAILED: %s" % fails if fails else "ALL OK"); sys.exit(1 if fails else 0)
