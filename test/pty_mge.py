#!/usr/bin/env python3
"""mge: animated steam locomotive on a live daemon through a real PTY.
Usage: unshare -n -u -m python3 test/pty_mge.py [binary]"""
import os, pty, select, subprocess, sys, tempfile, time

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
BIN = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "dist", "necorouter")
root = tempfile.mkdtemp(prefix="nr.", dir="/tmp")
os.makedirs(root + "/etc/necorouter")
open(root + "/etc/necorouter/startup-config", "w").write(
    "hostname lab-rtr\nend\n")
env = dict(os.environ, NECOROUTER_ROOT=root)
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
        r, _, _ = select.select([fd], [], [], 0.05)
        if r:
            try: c = os.read(fd, 65536)
            except OSError: break
            if not c: break
            out += c
    return out.decode(errors="replace")
def send(s, t=0.6): os.write(fd, s.encode()); return read(t)
fails = []
def check(n, ok, det=""):
    print(("PASS " if ok else "FAIL ") + n + ("" if ok else " -> " + det))
    if not ok: fails.append(n)
read(1.5)
o = send("?", 0.5)
check("mge listed by ?", "mge" in o, repr(o[:300]))
send("\x15")
o = send("mg\t", 0.4)
check("Tab completes mg -> mge", "mge " in o, repr(o))
send("\x15")
# full run
t0 = time.time()
o = send("mge\r", 0.5)
check("animation starts: cursor hidden, train entering", "\x1b[?25l" in o and "|" in o, repr(o[:200]))
o += read(6.0)
dur = time.time() - t0
check("train drives through and CLI returns", "Have a nice day" in o and "lab-rtr>" in o, repr(o[-120:]))
check("cursor restored", o.rfind("\x1b[?25h") > o.rfind("\x1b[?25l"))
check("many animated frames (cursor-up redraws)", o.count("\x1b[13A") >= 20, str(o.count("\x1b[13A")))
check("wheels spin ((+) and (x) both seen)", "(+)" in o and "(x)" in o)
# interruption
o = send("mge\r", 0.8)
o = send("\x03", 1.0)
check("Ctrl-C stops the train, prompt returns", "lab-rtr>" in o and "\x1b[?25h" in o, repr(o[-150:]))
time.sleep(0.4); tail = read(0.4)
check("no more frames after Ctrl-C", "COAL" not in tail, repr(tail[:100]))
o = send("enable\r", 0.6)
check("CLI usable after interrupt", "lab-rtr#" in o, repr(o))
o = send("mge ?", 0.6)
check("mge ? shows the TEXT argument hint", "TEXT" in o, repr(o[-200:]))
send("\x15")
o = send("mge NECO\r", 1.0)
check("mge NECO: custom tender label", "NECO" in o and "COAL" not in o, repr(o[-120:]))
o = send("\x03", 1.0)
check("label train stopped, prompt back", "lab-rtr#" in o, repr(o[-120:]))
try: os.kill(pid, 9)
except Exception: pass
d.terminate(); d.wait()
print("FAILED: %s" % fails if fails else "ALL OK"); sys.exit(1 if fails else 0)
