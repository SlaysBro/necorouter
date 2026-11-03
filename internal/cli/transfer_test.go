package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSSH replaces scp and sftp in PATH: saves arguments and environment, "uploads" a file to $FAKE/uploaded
// and "downloads" $FAKE/source. Returns the FAKE directory.
func fakeSSH(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	scp := `#!/bin/sh
echo "$@" > "$FAKE/args"
env > "$FAKE/env"
[ -f "$FAKE/fail" ] && { cat "$FAKE/fail" >&2; exit 1; }
for a; do prev2=$prev; prev=$last; last=$a; done
case "$last" in
  *@*) cp "$prev" "$FAKE/uploaded" ;;
  *) cp "$FAKE/source" "$last" ;;
esac
`
	sftp := `#!/bin/sh
echo "$@" > "$FAKE/args"
env > "$FAKE/env"
cat > "$FAKE/stdin"
[ -f "$FAKE/fail" ] && { cat "$FAKE/fail" >&2; exit 1; }
set -- $(head -1 "$FAKE/stdin")
case "$1" in
  put) cp "$2" "$FAKE/uploaded" ;;
  get) cp "$FAKE/source" "$3" ;;
esac
`
	bin := filepath.Join(dir, "bin")
	_ = os.MkdirAll(bin, 0o755)
	_ = os.WriteFile(bin+"/scp", []byte(scp), 0o755)
	_ = os.WriteFile(bin+"/sftp", []byte(sftp), 0o755)
	t.Setenv("FAKE", dir)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	return dir
}

func readFake(t *testing.T, dir, name string) string {
	b, _ := os.ReadFile(filepath.Join(dir, name))
	return string(b)
}

func TestSendStartupOverSSH(t *testing.T) {
	fake := fakeSSH(t)
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	if out := runCmd(s, "copy startup-config scp admin@10.0.0.1:"); !strings.Contains(out, "%") {
		t.Fatalf("ssh copy visible before feature ssh-copy: %q", out)
	}
	enableServices(s, "ssh-copy")
	runCmd(s, "configure terminal")
	runCmd(s, "hostname alpha")
	runCmd(s, "end")
	if out := runCmd(s, "copy startup-config scp admin@10.0.0.1:"); !strings.Contains(out, "startup-config not found") {
		t.Fatalf("no startup yet: %q", out)
	}
	runCmd(s, "write")
	for _, bad := range []string{"copy startup-config scp admin@h:/a_b c", "copy startup-config scp -oProxyCommand=x@h:y", "copy startup-config scp host:/x", "copy startup-config scp a@b:/x;reboot"} {
		if out := runCmd(s, bad); !strings.Contains(out, "%") || s.PromptSecret() {
			t.Fatalf("%q accepted: %q", bad, out)
		}
	}
	// password: asked without echo, goes to scp via SSH_ASKPASS, is stored nowhere
	runCmd(s, "copy startup-config scp admin@10.0.0.1:")
	if !s.PromptSecret() || !strings.Contains(s.Prompt(), "Password for admin@10.0.0.1") {
		t.Fatalf("no password prompt: %q", s.Prompt())
	}
	out := runCmd(s, "S3cretPw")
	if !strings.Contains(out, "[OK]") || !strings.Contains(out, "as startup_backup_alpha_") {
		t.Fatalf("not sent: %q", out)
	}
	st, _, err := readBackup(fake + "/uploaded")
	if err != nil || !strings.Contains(st, "hostname alpha") {
		t.Fatalf("uploaded archive wrong: %v %q", err, st)
	}
	args, env := readFake(t, fake, "args"), readFake(t, fake, "env")
	if !strings.Contains(args, "StrictHostKeyChecking=accept-new") || !strings.Contains(args, "known_hosts") ||
		!strings.Contains(args, "BatchMode=no") || strings.Contains(args, "S3cretPw") || !strings.Contains(args, "admin@10.0.0.1:startup_backup_alpha_") {
		t.Fatalf("scp arguments: %s", args)
	}
	if !strings.Contains(env, "NECOROUTER_ASKPASS=S3cretPw") || !strings.Contains(env, "SSH_ASKPASS_REQUIRE=force") {
		t.Fatalf("askpass environment missing")
	}
	for _, f := range []string{r.Cfg.Render(), readFake(t, filepath.Dir(r.P.Startup()), "startup-config")} {
		if strings.Contains(f, "S3cretPw") {
			t.Fatal("password leaked into a configuration")
		}
	}
	// without a password — keys only; directory and file name
	runCmd(s, "copy startup-config scp admin@10.0.0.1:/tmp/")
	out = runCmd(s, "")
	if !strings.Contains(out, "[OK]") || !strings.Contains(readFake(t, fake, "args"), "BatchMode=yes") || strings.Contains(readFake(t, fake, "env"), "NECOROUTER_ASKPASS") ||
		!strings.Contains(readFake(t, fake, "args"), "admin@10.0.0.1:/tmp/startup_backup_alpha_") {
		t.Fatalf("keys-only send: %q\n%s", out, readFake(t, fake, "args"))
	}
	runCmd(s, "copy startup-config sftp admin@10.0.0.1:/tmp/my.tar.gz")
	out = runCmd(s, "pw")
	if !strings.Contains(out, "as /tmp/my.tar.gz") || !strings.Contains(readFake(t, fake, "stdin"), " /tmp/my.tar.gz\nquit") {
		t.Fatalf("sftp send: %q\n%s", out, readFake(t, fake, "stdin"))
	}
	// transfer failure
	_ = os.WriteFile(fake+"/fail", []byte("Permission denied (publickey,password).\n"), 0o644)
	runCmd(s, "copy startup-config scp admin@10.0.0.1:")
	out = runCmd(s, "")
	if strings.Contains(out, "[OK]") || !strings.Contains(out, "scp failed: Permission denied") {
		t.Fatalf("failure not reported: %q", out)
	}
	os.Remove(fake + "/fail")
}

func TestSendBackupOverSSH(t *testing.T) {
	fake := fakeSSH(t)
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	runCmd(s, "write")
	runCmd(s, "copy startup-config storage description x")
	names := listBackups(r.P.Backups())
	if len(names) != 1 {
		t.Fatalf("no backup: %v", names)
	}
	if out := runCmd(s, "copy scp backup nope admin@h:"); !strings.Contains(out, "invalid backup name") {
		t.Fatalf("bad name: %q", out)
	}
	runCmd(s, "copy scp backup "+names[0]+" admin@10.0.0.1:/home/admin/")
	out := runCmd(s, "pw")
	if !strings.Contains(out, "[OK]") || !strings.Contains(readFake(t, fake, "args"), "admin@10.0.0.1:/home/admin/"+names[0]) {
		t.Fatalf("backup not sent: %q\n%s", out, readFake(t, fake, "args"))
	}
}

func TestReceiveStartupOverSSH(t *testing.T) {
	fake := fakeSSH(t)
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	enableServices(s, "ssh-copy")
	runCmd(s, "write")
	before, _ := os.ReadFile(r.P.Startup())
	if out := runCmd(s, "copy scp startup-config admin@h:/tmp/"); !strings.Contains(out, "must name a file") {
		t.Fatalf("directory source accepted: %q", out)
	}
	recv := func(proto string) string {
		runCmd(s, "copy "+proto+" startup-config admin@10.0.0.2:/cfg/remote.tar.gz")
		return runCmd(s, "pw")
	}
	// archive with a good configuration: warning, confirmation, write
	arc, _ := makeBackup("hostname remote\n!\nend\n", "", time.Now())
	_ = os.WriteFile(fake+"/source", arc, 0o600)
	out := recv("scp")
	if !strings.Contains(out, "will be overwritten") || s.Prompt() != "Continue? [y/N] " {
		t.Fatalf("no confirmation: %q", out)
	}
	if out := runCmd(s, "n"); !strings.Contains(out, "Aborted") {
		t.Fatalf("abort: %q", out)
	}
	if now, _ := os.ReadFile(r.P.Startup()); string(now) != string(before) {
		t.Fatal("startup changed without confirmation")
	}
	recv("sftp")
	if out := runCmd(s, "y"); !strings.Contains(out, "[OK]") {
		t.Fatalf("not applied: %q", out)
	}
	if now, _ := os.ReadFile(r.P.Startup()); !strings.Contains(string(now), "hostname remote") {
		t.Fatalf("startup not replaced:\n%s", now)
	}
	if r.Cfg.Hostname == "remote" {
		t.Fatal("running-config must not change")
	}
	// a plain text file is accepted too
	_ = os.WriteFile(fake+"/source", []byte("hostname plain\n"), 0o600)
	recv("scp")
	runCmd(s, "y")
	if now, _ := os.ReadFile(r.P.Startup()); !strings.Contains(string(now), "hostname plain") {
		t.Fatalf("plain file not accepted:\n%s", now)
	}
	// a file with errors, a corrupted archive and binary garbage are not accepted, startup does not change
	keep, _ := os.ReadFile(r.P.Startup())
	bad := map[string][]byte{"errors": []byte("hostname x\nbogus line\n"), "binary": {0, 1, 2, 3}, "damaged": append([]byte{}, arc...)}
	bad["damaged"][len(arc)/2] ^= 0xff
	for name, b := range bad {
		_ = os.WriteFile(fake+"/source", b, 0o600)
		out := recv("scp")
		if strings.Contains(out, "[OK]") || s.Prompt() == "Continue? [y/N] " {
			t.Fatalf("%s accepted: %q", name, out)
		}
	}
	if now, _ := os.ReadFile(r.P.Startup()); string(now) != string(keep) {
		t.Fatal("startup changed by a bad file")
	}
	// output of the remote side's error
	_ = os.WriteFile(fake+"/fail", []byte("Host key verification failed.\n"), 0o644)
	if out := recv("scp"); !strings.Contains(out, "Host key verification failed") {
		t.Fatalf("remote error not shown: %q", out)
	}
}
