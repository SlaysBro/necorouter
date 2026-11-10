#!/usr/bin/env python3
"""Check of the interactive CLI (bare `necorouter shell`, as after
login/sshd) through a real PTY: Tab, ?, history, Ctrl-Z, exit. Run inside: unshare -n -u -m python3 test/pty_cli.py"""
import os, pty, re, select, subprocess, sys, tempfile, time

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

status = None  # exit status of the login shell; waitpid(WNOHANG) reaps the child, so it is collected once
def shell_exited():
    global status
    if status is None:
        try:
            p, st = os.waitpid(pid, os.WNOHANG)
        except ChildProcessError:
            return True
        if p == pid: status = st
    return status is not None

buf = b""
def read(t=1.0):
    global buf
    out = b""
    end = time.time() + t
    while time.time() < end:
        r, _, _ = select.select([fd], [], [], 0.1)
        if r:
            try: c = os.read(fd, 4096)
            except OSError: break
            if not c: break
            out += c
    buf += out
    return out.decode(errors="replace")
def send(s, t=0.6):
    os.write(fd, s.encode()); return read(t)

fails = []
def check(name, ok, detail=""):
    print(("PASS " if ok else "FAIL ") + name + ("" if ok else "  -> " + detail))
    if not ok: fails.append(name)

o = read(1.5)
check("shell starts at the prompt, hostname from startup-config (no own login)", "lab-rtr>" in o and "login:" not in o and "Password" not in o, repr(o))

o = send("?")
check("? lists commands without Enter", "enable" in o and "show" in o and "ping" in o, repr(o))
o = send("\x15")                       # Ctrl-U clears the line
o = send("en\t")
check("Tab completes 'en' -> 'enable '", "enable " in o, repr(o))
o = send("\r", 0.8)
check("enable -> privileged prompt", "lab-rtr#" in o, repr(o))
o = send("conf\t")
check("Tab completes 'conf' -> 'configure '", "configure " in o, repr(o))
o = send("t\t")
check("Tab completes 'terminal '", "terminal " in o, repr(o))
o = send("\r", 0.8)
check("enter config mode", "lab-rtr(config)#" in o, repr(o))
o = send("interface\t")
check("Tab after 'interface' adds space", "interface " in o, repr(o))
o = send("isp\r", 0.8)
check("enter interface mode", "lab-rtr(config-if)#" in o, repr(o))
o = send(" ip ?")
check("? in interface mode shows ip subcommands", "address" in o and "nat" in o and "tunnel" in o and "ospf" in o, repr(o))
o = send("\x15")
o = send("ip address 10.1.1.1/30\r", 0.8)
o = send("ip addr\x1b[A", 0.3)         # Up -> the previous command is filled in
o = send("\x15")
o = send("bogus\r", 0.8)
check("invalid input is reported", "Invalid input detected" in o, repr(o))
o = send("ip nat \t")
check("Tab on ambiguous prefix lists the variants without descriptions", "inside" in o and "outside" in o and "Configure" not in o, repr(o))
o = send("\x15")
o = send("ip nat ?")
check("? lists inside/outside after ambiguous prefix", "inside" in o and "outside" in o, repr(o))
o = send("\x15")
o = send("\x1a", 0.8)                  # Ctrl-Z == end
check("Ctrl-Z returns to privileged mode", "lab-rtr#" in o, repr(o))
o = send("sh run\r", 0.8)
check("'sh run' abbreviation prints running-config", "interface isp" in o and "ip address 10.1.1.1/30" in o, repr(o))
o = send("sh ip int br\r", 0.8)
check("'sh ip int br' works", "isp" in o and "10.1.1.1/30" in o, repr(o))
o = send("show ip ro?")
check("? with partial word completes options", "route" in o, repr(o))
o = send("\x15")
# Ctrl-D on an empty line: leaves modes one at a time, leaves the shell only from the base level
o = send("configure terminal\r", 0.8)
o = send("interface isp\r", 0.8)
o = send("\x04", 0.8)
check("Ctrl-D in interface mode goes back to config mode", "lab-rtr(config)#" in o and not shell_exited(), repr(o))
o = send("\x04", 0.8)
check("Ctrl-D in config mode goes back to privileged mode", "lab-rtr#" in o and "(config" not in o.split("lab-rtr#")[-1], repr(o))
o = send("abc", 0.3)
o = send("\x04", 0.3)                  # non-empty line: Ctrl-D deletes a character instead of exiting
o = send("\x15", 0.3)
check("Ctrl-D on a non-empty line does not log out", not shell_exited(), repr(o))
o = send("\x04", 1.2)
time.sleep(0.3)
shell_exited()
check("Ctrl-D at the base level ends the login shell (getty then shows the login prompt again)", status is not None and os.WIFEXITED(status) and os.WEXITSTATUS(status) == 0, repr(status))

d.terminate(); d.wait()
if fails:
    print("--- necorouter daemon log ---\n" + open(root + "/d.log").read())
print("\nFAILED: %s" % fails if fails else "\nALL CLI CHECKS PASSED")
sys.exit(1 if fails else 0)
