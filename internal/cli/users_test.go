package cli

import (
	"strings"
	"testing"

	"necorouter/internal/config"
)

// Passwords live only in Linux accounts: they do not get into the configuration.
func TestPasswordNeverStoredInConfig(t *testing.T) {
	r := newTestRouter(t)
	// old configuration with a password: the line is ignored, there is no password in running-config
	if errs := r.Replay("username neto\n password OldSecret\n role noc\n"); len(errs) > 0 {
		t.Fatal(errs)
	}
	out := r.Cfg.Render()
	mustContain(t, out, "username neto", " role noc")
	mustNotContain(t, out, "OldSecret", "password")
	if u := r.Lookup("admin"); u == nil || u.Role != "admin" {
		t.Fatal("built-in admin must exist")
	}
	mustNotContain(t, out, "username admin")
}

func TestPasswordCommandChangesLinuxAccountOnly(t *testing.T) {
	r := newTestRouter(t)
	s := NewSession(r, &config.User{Name: "a", Role: "admin"})
	var sb strings.Builder
	s.Exec("enable", &sb)
	s.Exec("configure terminal", &sb)
	s.Exec("username nosuchlinux", &sb)
	sb.Reset()
	s.Exec("password S3cret", &sb)
	// there is no such account in the system (services are disabled in the test) — the command honestly says so
	if !strings.Contains(sb.String(), "no router account") {
		t.Fatalf("expected an explicit error, got %q", sb.String())
	}
	if strings.Contains(r.Cfg.Render(), "S3cret") {
		t.Fatal("password leaked into running-config")
	}
	if !strings.Contains(s.Help("password "), "PASSWORD") {
		t.Fatal("password argument hint missing")
	}
}

// A user without a role does not get privileged mode; noc and helpdesk enter it as in EcoRouter.
func TestNoRoleCannotEnablePrivileged(t *testing.T) {
	r := newTestRouter(t)
	s := NewSession(r, &config.User{Name: "x", Role: ""})
	var sb strings.Builder
	s.Exec("enable", &sb)
	if !strings.Contains(sb.String(), "access denied") {
		t.Fatalf("role without a role must not enter privileged mode: %q", sb.String())
	}
	for _, role := range []string{"noc", "helpdesk"} {
		s := NewSession(r, &config.User{Name: "x", Role: role})
		var sb strings.Builder
		s.Exec("enable", &sb)
		if sb.Len() != 0 {
			t.Fatalf("role %q must enter privileged mode: %q", role, sb.String())
		}
	}
	s = NewSession(r, &config.User{Name: "x", Role: "admin"})
	sb.Reset()
	s.Exec("enable", &sb)
	if sb.Len() != 0 {
		t.Fatalf("admin must enter privileged mode: %q", sb.String())
	}
}

// The startup configuration is shown to the admin role only (it holds the secrets in clear).
func TestShowConfigRequiresAdmin(t *testing.T) {
	r := newTestRouter(t)
	for _, role := range []string{"", "noc", "helpdesk"} {
		s := NewSession(r, &config.User{Name: "x", Role: role})
		for _, c := range []string{"show startup-config"} {
			var sb strings.Builder
			s.Exec(c, &sb)
			if !strings.Contains(sb.String(), "%") || strings.Contains(sb.String(), "hostname") {
				t.Fatalf("role %q: %q must be unavailable, got %q", role, c, sb.String())
			}
		}
		var sb strings.Builder
		s.Exec("show version", &sb)
		if strings.Contains(sb.String(), "denied") {
			t.Fatal("show version must stay available")
		}
	}
	s := NewSession(r, &config.User{Name: "a", Role: "admin"})
	var sb strings.Builder
	s.Exec("show running-config", &sb)
	if strings.Contains(sb.String(), "denied") || sb.Len() == 0 {
		t.Fatalf("admin must see the config: %q", sb.String())
	}
}
