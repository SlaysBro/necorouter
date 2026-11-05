package cli

import (
	"fmt"
	"strings"

	"necorouter/internal/config"
)

// banner motd {TEXT | default} / no banner motd / show banner motd — the greeting the user
// sees after logging in to the CLI (before the prompt). By default it is a string with the program version.
// The banner before the password prompt (ssh and /etc/issue) is a separate file, /etc/necorouter/banner.

const maxMotd = 255

// motdText returns the current greeting ("" — no greeting).
func motdText(c *config.Config) string {
	switch {
	case c.BannerOff:
		return ""
	case c.BannerMotd != "":
		return c.BannerMotd
	}
	return "necorouter version " + VersionString()
}

// Motd — the post-login greeting for a new session.
func (r *Router) Motd() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return motdText(r.Cfg)
}

func bannerConfigNode() *Node {
	set := func(s *Session, v []string) error {
		t := strings.TrimSpace(v[0])
		switch {
		case t == "":
			return fmt.Errorf("the message must not be empty (use 'no banner motd' to remove it)")
		case len(t) > maxMotd:
			return fmt.Errorf("the message is too long (up to %d characters)", maxMotd)
		case strings.ContainsAny(t, "\r\n"):
			return fmt.Errorf("the message must be a single line")
		}
		s.cfg().BannerMotd, s.cfg().BannerOff = t, false
		return nil
	}
	return K("banner", "Define a login banner",
		K("startup", "Show the configured banner before console and SSH login").R(func(s *Session, _ []string) error {
			s.cfg().BannerStartupOff = false
			return nil
		}).N(func(s *Session, _ []string) error {
			s.cfg().BannerStartupOff = true
			return nil
		}),
		K("motd", "Message of the day, shown after login",
			K("default", "Default message: the program version").R(func(s *Session, _ []string) error {
				s.cfg().BannerMotd, s.cfg().BannerOff = "", false
				return nil
			}),
			A(KRest, "TEXT", "Message of the day (one line)").R(set),
		).N(func(s *Session, _ []string) error {
			s.cfg().BannerMotd, s.cfg().BannerOff = "", true
			return nil
		}))
}

func showBannerNode() *Node {
	return K("banner", "Show the login banner",
		K("motd", "Show the current message of the day").R(func(s *Session, _ []string) error {
			if m := motdText(s.cfg()); m != "" {
				s.Printf("%s\n", m)
			}
			return nil
		}))
}
