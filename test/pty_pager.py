#!/usr/bin/env python3
"""show pager: scrolling, q/Ctrl-C, terminal length.
Usage: unshare -n -u -m python3 test/pty_pager.py [binary]"""
import os, pty, select, subprocess, sys, tempfile, time

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
BIN = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "dist", "necorouter")
root = tempfile.mkdtemp(prefix="nr.", dir="/tmp")
os.makedirs(root + "/etc/necorouter")
open(root + "/etc/necorouter/startup-config", "w").write(
    "hostname lab-rtr\n" + "".join("ip nat pool P%d 10.0.%d.1-10.0.%d.9\n" % (i, i, i) for i in range(1, 61)) +
    "end\n")
env = dict(os.environ, NECOROUTER_ROOT=root)
d = subprocess.Popen([BIN, "daemon"], env=env, stderr=open(root + "/d.log", "w"))
for _ in range(50):
    if os.path.exists(root + "/run/necorouter/ctl.sock"): break
    time.sleep(0.1)
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
import fcntl, struct, termios
fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack('HHHH', 10, 80, 0, 0))
read(1.5)
o = send("show hostname\r", 0.6)
check("short show is not paged", "More" not in o and "lab-rtr" in o, repr(o[-150:]))
o = send("show running-config\r", 1.0)
check("long show: first page + More", "--More--" in o and "hostname lab-rtr" in o, repr(o[-250:]))
check("first page does not run to the end", "P60" not in o, repr(o[-150:]))
o = send(" ", 0.5)
check("space: next page", "--More--" in o, repr(o[-200:]))
o = send("b", 0.5)
check("b: page back", "--More--" in o or "hostname" in o, repr(o[-200:]))
o = send("G", 0.5)
check("G: end of output", "(END)" in o and "P60" in o, repr(o[-250:]))
o = send("q", 0.6)
check("q quits, prompt returns", "lab-rtr>" in o and "--More--" not in o.split("lab-rtr>")[-1], repr(o[-200:]))
send("show running-config\r", 1.0)
o = send("\x03", 0.6)
check("Ctrl-C quits pager, prompt returns", "lab-rtr>" in o, repr(o[-200:]))
o = send("show running-config\r", 1.0)
o = send("/P30\r", 0.6)
check("/ search jumps to match", "P30" in o, repr(o[-250:]))
send("q", 0.5)
o = send("terminal length 0\r", 0.6)
o = send("show running-config\r", 1.2)
check("terminal length 0 disables paging", "--More--" not in o and "P60" in o, repr(o[-200:]))
o = send("terminal length 5\r", 0.6)
o = send("show running-config\r", 1.0)
check("terminal length 5 -> 5 lines per page", o.count("\r\n") < 12 and "--More--" in o, repr(o[-250:]))
send("q", 0.5)
o = send("enable\r", 0.6)
check("CLI usable afterwards", "lab-rtr#" in o, repr(o))
try: os.kill(pid, 9)
except Exception: pass
d.terminate(); d.wait()
print("FAILED: %s" % fails if fails else "ALL OK"); sys.exit(1 if fails else 0)
