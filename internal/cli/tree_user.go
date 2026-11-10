package cli

import (
	"fmt"
	"necorouter/internal/config"
)

func userTree() []*Node {
	u := func(f func(u *config.User, v []string) error) Handler {
		return func(s *Session, v []string) error {
			x := s.cfg().FindUser(s.top().obj)
			if x == nil {
				return fmt.Errorf("user no longer exists")
			}
			return f(x, v)
		}
	}
	return []*Node{
		K("password", "Set the account password (kept only in the Linux account, not in the configuration)",
			A(KRest, "PASSWORD", "Password (any text up to the end of the line)").R(func(s *Session, v []string) error {
				name := s.top().obj
				if s.cfg().FindUser(name) == nil {
					return fmt.Errorf("user no longer exists")
				}
				if s.noApply { // replaying a configuration does not change the Linux account password
					return nil
				}
				return s.R.Mgr.SetPassword(name, v[0])
			})).Sec(),
		K("role", "Set the user role",
			A(KWord, "ROLE", "Role: admin (full privileges), noc or helpdesk").Cmp(func(*Session) []string { return config.Roles }).
				R(u(func(x *config.User, v []string) error { x.Role = v[0]; return nil }))).
			N(u(func(x *config.User, _ []string) error { x.Role = ""; return nil })),
	}
}

func userNames(s *Session) []string {
	var o []string
	for _, u := range s.cfg().Users {
		o = append(o, u.Name)
	}
	return o
}

// roleOK — valid user roles.
