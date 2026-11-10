#!/usr/bin/env python3
"""show log follow: live page (FOLLOW/PAUSED), scrolling, G/End, q and Ctrl-C.
Usage: unshare -n -u -m python3 test/pty_follow.py [binary]"""
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

def open_shell():
    pid, fd = pty.fork()
    if pid == 0:
        os.execve(BIN, [BIN, "shell"], env)
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", 12, 80, 0, 0))
    return pid, fd

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

fails = []
def check(name, ok, det=""):
    print(("PASS " if ok else "FAIL ") + name + ("" if ok else " -> " + repr(det)[-300:]))
    if not ok:
        fails.append(name)

pid1, fd1 = open_shell()
read(fd1, 1.5)
# admin login is done by the shell; the test session is in privileged mode
os.write(fd1, b"enable\r"); read(fd1, 0.5)
# enough journal lines that the page scrolls: each configuration change writes audit lines
pid2, fd2 = open_shell(); read(fd2, 1.5)
os.write(fd2, b"enable\r"); read(fd2, 0.5)
for i in range(25):
    os.write(fd2, b"configure terminal\r"); read(fd2, 0.15)
    os.write(fd2, b"hostname seed%d\r" % i); read(fd2, 0.15)
    os.write(fd2, b"end\r"); read(fd2, 0.15)
os.write(fd1, b"show log follow\r")
o = read(fd1, 1.0)
check("follow opens a live page", "FOLLOW" in o, o)

# new messages come from the other session: a configuration change is journalled
os.write(fd2, b"configure terminal\r"); read(fd2, 0.4)
os.write(fd2, b"hostname follow-test\r"); read(fd2, 0.4)
os.write(fd2, b"end\r"); read(fd2, 0.6)
o = read(fd1, 1.5)
check("new lines arrive on the open page", "follow-test" in o or "hostname" in o, o)
check("page still follows the end", "FOLLOW" in o, o)

os.write(fd1, b"\x1b[A"); o = read(fd1, 0.6)  # Up: scroll up, pause
check("scrolling up pauses (PAUSED)", "PAUSED" in o, o)
os.write(fd2, b"enable\r"); read(fd2, 0.3)
os.write(fd2, b"configure terminal\r"); read(fd2, 0.4)
os.write(fd2, b"hostname follow-test2\r"); read(fd2, 0.4)
os.write(fd2, b"end\r"); read(fd2, 0.6)
o = read(fd1, 0.8)
check("paused view is not pulled down", "PAUSED" in o, o)

os.write(fd1, b"G"); o = read(fd1, 0.6)  # G: back to the end
check("G returns to the end (FOLLOW)", "FOLLOW" in o, o)

os.write(fd1, b"q"); o = read(fd1, 0.8)
check("q leaves the page", "FOLLOW" not in o and "PAUSED" not in o, o)
os.write(fd1, b"show hostname\r"); o = read(fd1, 0.8)
check("console usable after q", "follow-test" in o or "hostname" in o, o)

# Ctrl-C variant
os.write(fd1, b"show log follow\r"); read(fd1, 1.0)
os.write(fd1, b"\x03"); o = read(fd1, 0.8)
os.write(fd1, b"show hostname\r"); o = read(fd1, 0.8)
check("Ctrl-C leaves the page and the console works", "hostname" in o, o)

for fd, pid in [(fd1, pid1), (fd2, pid2)]:
    try:
        os.write(fd, b"exit\r")
    except OSError:
        pass
d.terminate()
if fails:
    print("FAILED:", ", ".join(fails))
    sys.exit(1)
print("ALL OK")
