package cli

import "necorouter/internal/config"

func removeStr(l []string, v string) []string {
	var out []string
	for _, x := range l {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func delRoute(s *Session, prefix, nh string) error {
	p, err := config.Canon(prefix)
	if err != nil {
		return err
	}
	c := s.cfg()
	var out []config.Route
	for _, r := range c.Routes {
		if r.Prefix == p && (nh == "" || r.NextHop == nh) {
			continue
		}
		out = append(out, r)
	}
	c.Routes = out
	return nil
}

func contains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

func addOnce(l *[]string, v string) {
	if !contains(*l, v) {
		*l = append(*l, v)
	}
}

func nameOK(s string) bool { return nameRe.MatchString(s) }
