package cli

import (
	"fmt"

	"necorouter/internal/config"
)

func servicePolicyNames(s *Session) []string {
	var out []string
	for _, p := range s.cfg().ServicePolicies {
		out = append(out, p.Name)
	}
	return out
}

// service-policy follows the router limiter grammar. Policies apply to a
// service-instance in the in/out direction.
func servicePolicyNode() *Node {
	name := A(KWord, "NAME", "Policy name (letters, digits, _ . : / -)").
		Chk(nameOK).Cmp(servicePolicyNames).R(func(s *Session, v []string) error {
		p := s.cfg().FindServicePolicy(v[0])
		if p == nil {
			p = &config.ServicePolicy{Name: v[0]}
			s.cfg().ServicePolicies = append(s.cfg().ServicePolicies, p)
		}
		s.push("qospolicy", p.Name)
		return nil
	})
	return K("service-policy", "Create or configure a traffic service-policy", name).
		N(func(s *Session, v []string) error {
			name := v[0]
			var out []*config.ServicePolicy
			for _, p := range s.cfg().ServicePolicies {
				if p.Name != name {
					out = append(out, p)
				}
			}
			s.cfg().ServicePolicies = out
			for _, port := range s.cfg().Ports {
				for _, si := range port.SIs {
					if si.PolicyIn == name {
						si.PolicyIn = ""
					}
					if si.PolicyOut == name {
						si.PolicyOut = ""
					}
				}
			}
			return nil
		})
}

func servicePolicyTree() []*Node {
	set := func(unit string, min, max int) *Node {
		return K(unit, "Bandwidth value in "+unit,
			A(KInt, "VALUE", "Maximum bandwidth").Rng(min, max).R(func(s *Session, v []string) error {
				p := s.cfg().FindServicePolicy(s.top().obj)
				if p == nil {
					return fmt.Errorf("service-policy no longer exists")
				}
				fmt.Sscan(v[0], &p.BandwidthValue)
				p.BandwidthUnit = unit
				return nil
			}))
	}
	return []*Node{
		K("bandwidth", "Set the maximum bandwidth",
			set("gbps", 1, 100),
			set("mbps", 1, 100000),
			set("kbps", 64, 100000000),
			set("percent", 1, 100),
		).N(func(s *Session, _ []string) error {
			p := s.cfg().FindServicePolicy(s.top().obj)
			if p == nil {
				return fmt.Errorf("service-policy no longer exists")
			}
			p.BandwidthUnit, p.BandwidthValue = "", 0
			return nil
		}),
	}
}

func bindServicePolicyNode() *Node {
	set := func(dir string) Handler {
		return func(s *Session, v []string) error {
			if s.cfg().FindServicePolicy(v[0]) == nil {
				return fmt.Errorf("service-policy %s does not exist", v[0])
			}
			si, err := currentSI(s)
			if err != nil {
				return err
			}
			if dir == "in" {
				si.PolicyIn = v[0]
			} else {
				si.PolicyOut = v[0]
			}
			return nil
		}
	}
	clear := func(dir string) Handler {
		return func(s *Session, v []string) error {
			si, err := currentSI(s)
			if err != nil {
				return err
			}
			if dir == "in" && si.PolicyIn == v[0] {
				si.PolicyIn = ""
			}
			if dir == "out" && si.PolicyOut == v[0] {
				si.PolicyOut = ""
			}
			return nil
		}
	}
	name := A(KWord, "NAME", "Configured service-policy",
		K("in", "Apply to incoming traffic").R(set("in")).N(clear("in")),
		K("out", "Apply to outgoing traffic").R(set("out")).N(clear("out")),
	).Cmp(servicePolicyNames)
	return K("service-policy", "Apply a service-policy in the specified direction", name)
}

func currentSI(s *Session) (*config.SI, error) {
	if p := s.cfg().FindPort(s.top().obj2); p != nil {
		if si := p.FindSI(s.top().obj); si != nil {
			return si, nil
		}
	}
	return nil, fmt.Errorf("service-instance no longer exists")
}

func showServicePolicy(s *Session) error {
	if err := s.needAdmin(); err != nil {
		return err
	}
	if len(s.cfg().ServicePolicies) == 0 {
		s.Printf("No service-policies configured.\n")
		return nil
	}
	for _, p := range s.cfg().ServicePolicies {
		s.Printf("Service policy %s\n", p.Name)
		if p.BandwidthUnit != "" {
			s.Printf("  Bandwidth: %d %s\n", p.BandwidthValue, p.BandwidthUnit)
		}
	}
	return nil
}
