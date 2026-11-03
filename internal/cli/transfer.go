package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// Copying the configuration to another device and back over SSH:
//
//	copy startup-config (scp|sftp) USER@HOST[:PATH]        send the startup-config archive
//	copy (scp|sftp) backup NAME USER@HOST[:PATH]            send an archive from storage (show backups)
//	copy (scp|sftp) startup-config USER@HOST:FILE          receive an archive (or a plain configuration file)
//
// System scp and sftp are used. An empty PATH, or a path ending in "/", is a directory:
// on a non-necorouter device, the archive is placed in the home directory (or the given directory) under its own name.
// Authentication: by root's keys (~/.ssh) or by password — the password is asked without echo (empty input —
// keys only), passed to the scp/sftp process via SSH_ASKPASS and stored nowhere.
// Host keys: StrictHostKeyChecking=accept-new in /var/lib/necorouter/known_hosts (a new host is remembered,
// a changed key is refused).
var targetRe = regexp.MustCompile(`^([A-Za-z0-9_][A-Za-z0-9._-]{0,31})@((?:[A-Za-z0-9][A-Za-z0-9.-]{0,252})|(?:\[[0-9A-Fa-f:.]{2,45}\]))(?::([A-Za-z0-9._/~+-]{0,200}))?$`)

type sshTarget struct{ User, Host, Path string }

func parseTarget(s string) (sshTarget, error) {
	m := targetRe.FindStringSubmatch(s)
	if m == nil {
		return sshTarget{}, fmt.Errorf("invalid address %q: expected USER@HOST:PATH (path without spaces)", s)
	}
	return sshTarget{m[1], m[2], m[3]}, nil
}

func (t sshTarget) isDir() bool { return t.Path == "" || strings.HasSuffix(t.Path, "/") }

// remoteFile — the full path of the file on the remote side.
func (t sshTarget) remoteFile(name string) string {
	if t.isDir() {
		return t.Path + name
	}
	return t.Path
}

func (t sshTarget) String() string { return t.User + "@" + t.Host + ":" + t.Path }

func sshOpts(s *Session, password bool) []string {
	o := []string{
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=" + s.R.P.Var + "/known_hosts",
		"-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=5", "-o", "ServerAliveCountMax=3",
		"-o", "NumberOfPasswordPrompts=1",
	}
	if password {
		return append(o, "-o", "BatchMode=no", "-o", "PreferredAuthentications=publickey,password,keyboard-interactive")
	}
	return append(o, "-o", "BatchMode=yes")
}

// transferMax — the largest accepted file.
const transferMax = 8 << 20

// runSSH runs scp or sftp. direction: "put" (local → remote) or "get" (remote → local).
// The router lock is released for the duration of the transfer, Ctrl-C interrupts the transfer.
func runSSH(s *Session, proto string, t sshTarget, remote, local, direction, password string) error {
	bin := proto
	p, err := exec.LookPath(bin)
	if err != nil {
		return fmt.Errorf("%s is not installed", bin)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	opts := sshOpts(s, password != "")
	var args []string
	var stdin string
	switch proto {
	case "scp":
		args = append([]string{"-q"}, opts...)
		rem := t.User + "@" + t.Host + ":" + remote
		if direction == "put" {
			args = append(args, local, rem)
		} else {
			args = append(args, rem, local)
		}
	default: // sftp: commands from standard input
		args = append([]string{"-q"}, opts...)
		args = append(args, t.User+"@"+t.Host)
		if direction == "put" {
			stdin = fmt.Sprintf("put %s %s\nquit\n", local, remote)
		} else {
			stdin = fmt.Sprintf("get %s %s\nquit\n", remote, local)
		}
	}
	s.cmu.Lock()
	ctx := s.ctx
	s.cmu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.Command(p, args...)
	cmd.Env = append(os.Environ(), "SSH_ASKPASS="+exe, "SSH_ASKPASS_REQUIRE=force", "DISPLAY=necorouter:0")
	if password != "" {
		cmd.Env = append(cmd.Env, "NECOROUTER_ASKPASS="+password)
	}
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // no controlling terminal, so the password goes through SSH_ASKPASS
	if err := cmd.Start(); err != nil {
		return err
	}
	s.R.mu.Unlock()
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		case <-done:
		}
	}()
	werr := cmd.Wait()
	close(done)
	s.R.mu.Lock()
	if werr != nil {
		msg := strings.TrimSpace(out.String())
		if ctx.Err() != nil {
			msg = "interrupted or timed out"
		}
		if len(msg) > 400 {
			msg = msg[:400] + "..."
		}
		if msg == "" {
			msg = werr.Error()
		}
		return fmt.Errorf("%s failed: %s", proto, strings.ReplaceAll(msg, "\n", "\n% "))
	}
	return nil
}

// sshCopy — non-vanilla copy-over-SSH syntax: enabled by the feature ssh-copy command.
var sshCopy = featOn("ssh-copy")

func exportNodes() []*Node {
	mk := func(proto string) *Node {
		return K(proto, "Send the startup configuration to a remote device over SSH",
			A(KWord, "USER@HOST:PATH", "Destination; a path ending with / or empty is a directory (the archive keeps its name)").
				Chk(func(v string) bool { _, err := parseTarget(v); return err == nil }).
				R(func(s *Session, v []string) error { return cmdSendStartup(s, proto, v[0]) })).When(sshCopy)
	}
	return []*Node{mk("scp"), mk("sftp")}
}

// transferNodes: copy scp|sftp backup NAME TARGET and copy scp|sftp startup-config TARGET.
func transferNodes() []*Node {
	chk := func(v string) bool { _, err := parseTarget(v); return err == nil }
	mk := func(proto string) *Node {
		// copy scp backup — always available; the rest (sftp, receiving) — only with ssh-copy
		backup := K("backup", "Send a backup from the storage to a remote device",
			A(KWord, "NAME", "Backup name from 'show backups'",
				A(KWord, "USER@HOST:PATH", "Destination").Chk(chk).
					R(func(s *Session, v []string) error { return cmdSendBackup(s, proto, v[0], v[1]) })))
		recv := K("startup-config", "Receive the startup configuration from a remote device (archive or plain file)",
			A(KWord, "USER@HOST:FILE", "Source file").Chk(chk).
				R(func(s *Session, v []string) error { return cmdReceiveStartup(s, proto, v[0]) })).When(sshCopy)
		if proto == "sftp" {
			return K(proto, "Copy over SSH", backup, recv).When(sshCopy)
		}
		return K(proto, "Copy over SSH", backup, recv)
	}
	return []*Node{mk("scp"), mk("sftp")}
}

func withTemp(s *Session, f func(dir string) error) error {
	if err := os.MkdirAll(s.R.P.Var, 0o755); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(s.R.P.Var, "transfer-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	return f(dir)
}

func cmdSendStartup(s *Session, proto, target string) error {
	if err := s.needAdmin(); err != nil {
		return err
	}
	t, err := parseTarget(target)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(s.R.P.Startup())
	if err != nil {
		return fmt.Errorf("startup-config not found: save the configuration with 'write' first")
	}
	now := time.Now()
	data, err := makeBackup(string(b), "", now)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("startup_backup_%s_%s.tar.gz", backupHostName(s.cfg().Hostname), now.Format("2006-01-02_15-04-05"))
	return sendData(s, proto, t, name, data, "copy startup-config "+proto+" "+t.String())
}

func cmdSendBackup(s *Session, proto, name, target string) error {
	if err := s.needAdmin(); err != nil {
		return err
	}
	t, err := parseTarget(target)
	if err != nil {
		return err
	}
	if !backupNameRe.MatchString(name) {
		return fmt.Errorf("invalid backup name %q (see 'show backups')", name)
	}
	data, err := os.ReadFile(s.R.P.Backups() + "/" + name)
	if err != nil {
		return fmt.Errorf("backup %q not found", name)
	}
	return sendData(s, proto, t, name, data, "copy "+proto+" backup "+name+" "+t.String())
}

func sendData(s *Session, proto string, t sshTarget, name string, data []byte, jr string) error {
	if s.noApply {
		return fmt.Errorf("not available while replaying a configuration")
	}
	s.askSecret(fmt.Sprintf("Password for %s@%s (empty: keys only): ", t.User, t.Host), func(s *Session, pw string) error {
		return withTemp(s, func(dir string) error {
			local := dir + "/" + name
			if err := os.WriteFile(local, data, 0o600); err != nil {
				return err
			}
			if err := runSSH(s, proto, t, t.remoteFile(name), local, "put", pw); err != nil {
				return err
			}
			s.journal("%s", jr)
			s.Printf("[OK] %s sent to %s@%s as %s\n", name, t.User, t.Host, t.remoteFile(name))
			return nil
		})
	})
	return nil
}

func cmdReceiveStartup(s *Session, proto, target string) error {
	if err := s.needAdmin(); err != nil {
		return err
	}
	t, err := parseTarget(target)
	if err != nil {
		return err
	}
	if t.isDir() {
		return fmt.Errorf("the source must name a file, not a directory")
	}
	if s.noApply {
		return fmt.Errorf("not available while replaying a configuration")
	}
	s.askSecret(fmt.Sprintf("Password for %s@%s (empty: keys only): ", t.User, t.Host), func(s *Session, pw string) error {
		var text string
		err := withTemp(s, func(dir string) error {
			local := dir + "/incoming"
			if err := runSSH(s, proto, t, t.Path, local, "get", pw); err != nil {
				return err
			}
			st, err := os.Stat(local)
			if err != nil {
				return fmt.Errorf("nothing was received")
			}
			if st.Size() > transferMax {
				return fmt.Errorf("the file is too large")
			}
			if st2, _, err := readBackup(local); err == nil {
				text = st2
				return nil
			}
			b, err := os.ReadFile(local)
			if err != nil {
				return err
			}
			if !utf8.Valid(b) || strings.IndexByte(string(b), 0) >= 0 || (len(b) > 2 && b[0] == 0x1f && b[1] == 0x8b) {
				return fmt.Errorf("the file is neither a valid backup archive nor a text configuration")
			}
			text = string(b)
			return nil
		})
		if err != nil {
			return err
		}
		if _, errs := s.R.validateConfig(text); len(errs) > 0 {
			return fmt.Errorf("%s has errors, nothing was changed:\n%% %s", path.Base(t.Path), errList(errs, 5))
		}
		warn := fmt.Sprintf("%% Warning: startup-config will be overwritten with the configuration received from %s@%s.\n", t.User, t.Host)
		if s.cfg().Features["config-history"] {
			warn += "%          The old startup-config is kept in the history.\n"
		} else {
			warn += "%          The old startup-config will be lost.\n"
		}
		return s.ask(warn, "Continue?", func(s *Session) error {
			if err := s.R.writeStartup(text); err != nil {
				return err
			}
			s.journal("copy %s startup-config %s", proto, t.String())
			s.Printf("[OK] startup-config replaced. It takes effect when the daemon restarts; the running configuration is unchanged.\n")
			return nil
		})
	})
	return nil
}
