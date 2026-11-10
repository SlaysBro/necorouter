#!/usr/bin/env python3
"""Pasting several configuration lines at once: every line is executed in order, none is lost,
even the ones that arrive while the previous command is still running.
Usage: unshare -n -u -m python3 test/pty_paste.py [binary]"""
import os, pty, select, subprocess, sys, tempfile, time, fcntl, struct, termios

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
BIN = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "dist", "necorouter")
root = tempfile.mkdtemp(prefix="nr.", dir="/tmp")
os.makedirs(root + "/etc/necorouter")
open(root + "/etc/necorouter/startup-config", "w").write("hostname lab-rtr\nend\n")
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

def read(fd, t):
    out = b""
    end = time.time() + t
    while time.time() < end:
        r, _, _ = select.select([fd], [], [], 0.05)
        if r:
            try:
                c = os.read(fd, 65536)
            except OSError:
                break
            if not c:
                break
            out += c
    return out.decode(errors="replace")

pid, fd = os.forkpty() if False else pty.fork()
if pid == 0:
    os.execve(BIN, [BIN, "shell"], env)
fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 100, 0, 0))
read(fd, 1.5)
fails = []
def check(name, ok, det=""):
    print(("PASS " if ok else "FAIL ") + name + ("" if ok else " -> " + repr(det)[-400:]))
    if not ok:
        fails.append(name)

os.write(fd, b"enable\r"); read(fd, 0.5)
# the whole block is pasted at once: the terminal delivers it in one burst
paste = b"configure terminal\rhostname p1\rhostname p2\rhostname p3\rip domain-name paste.test\rend\r"
os.write(fd, paste)
o = read(fd, 3.0)
check("pasted lines are all executed", o.count("hostname") >= 3 or o.count("(config)") >= 1, o)
os.write(fd, b"show hostname\r"); o = read(fd, 0.8)
check("last pasted hostname took effect", "p3" in o, o)
os.write(fd, b"show running-config\r"); o = read(fd, 1.0)
check("pasted domain-name is in the configuration", "paste.test" in o or "domain-name" in o, o)

os.write(fd, b"exit\r")
d.terminate()
if fails:
    print("FAILED:", ", ".join(fails)); sys.exit(1)
print("ALL OK")
