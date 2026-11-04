package svc

import (
	"fmt"
	"hash/crc32"
	"net"
	"os"
	"strings"

	"necorouter/internal/config"
)

const (
	shapeUploadHandle   = "7a10:"
	shapeDownloadHandle = "7a11:"
)

type trafficShapeSvc struct{}

func (*trafficShapeSvc) Name() string { return "qos" }

func shapeIFB(dev string) string {
	return fmt.Sprintf("ifb-nr%04x", crc32.ChecksumIEEE([]byte(dev))&0xffff)
}
func shapeFilterPref(dev string) string {
	return fmt.Sprint(49152 + int(crc32.ChecksumIEEE([]byte(dev))%1024))
}
func shapeFilterHandle(dev string) string {
	return fmt.Sprintf("0x%x", crc32.ChecksumIEEE([]byte("necorouter:"+dev)))
}

type shapeTarget struct {
	download int
	upload   int
}

func (*trafficShapeSvc) Apply(env *Env, c *config.Config) ([]string, error) {
	var warns []string
	if env.St.TrafficShaping == nil {
		env.St.TrafficShaping = map[string]ShapeState{}
	}
	want := map[string]shapeTarget{}
	for _, port := range c.Ports {
		for _, si := range port.SIs {
			if si.PolicyIn == "" && si.PolicyOut == "" {
				continue
			}
			if si.Connect == "" {
				warns = append(warns, fmt.Sprintf("qos: service-instance %s/%s has a policy but no connected IP interface", port.Name, si.Name))
				continue
			}
			i := c.FindIface(si.Connect)
			if i == nil {
				continue
			}
			if i.Tunnel != nil || strings.HasPrefix(i.Name, "loopback") || i.Shutdown {
				warns = append(warns, fmt.Sprintf("qos: service-policy on %s requires an enabled physical or VLAN interface", i.Name))
				continue
			}
			dev := DevOf(env, c, i.Name)
			if dev == "" {
				warns = append(warns, fmt.Sprintf("qos: interface %s is not bound to a Linux device", i.Name))
				continue
			}
			target := want[dev]
			for _, binding := range []struct{ name, dir string }{{si.PolicyIn, "in"}, {si.PolicyOut, "out"}} {
				if binding.name == "" {
					continue
				}
				policy := c.FindServicePolicy(binding.name)
				if policy == nil || policy.BandwidthValue == 0 {
					warns = append(warns, fmt.Sprintf("qos: service-policy %s has no bandwidth", binding.name))
					continue
				}
				rate, err := servicePolicyRate(policy, dev)
				if err != nil {
					warns = append(warns, fmt.Sprintf("qos: service-policy %s on %s: %v", binding.name, dev, err))
					continue
				}
				if binding.dir == "in" {
					target.download = rate
				} else {
					target.upload = rate
				}
			}
			want[dev] = target
		}
	}

	if len(want) == 0 {
		for dev, old := range env.St.TrafficShaping {
			cleanupTrafficShape(dev, old)
			delete(env.St.TrafficShaping, dev)
		}
		return warns, nil
	}
	if !have("tc") {
		return append(warns, "qos: tc is not installed; install iproute2 to enable CAKE traffic shaping"), nil
	}
	needIP := false
	for _, target := range want {
		needIP = needIP || target.download > 0
	}
	if needIP && !have("ip") {
		return append(warns, "qos: ip is not installed; install iproute2 to shape download traffic"), nil
	}

	for dev, old := range env.St.TrafficShaping {
		if _, ok := want[dev]; !ok {
			cleanupTrafficShape(dev, old)
			delete(env.St.TrafficShaping, dev)
		}
	}

	for dev, target := range want {
		old := env.St.TrafficShaping[dev]
		next := old
		var errs []string
		if target.upload > 0 {
			if err := setCake(dev, shapeUploadHandle, target.upload, false); err != nil {
				errs = append(errs, fmt.Sprintf("upload on %s: %v", dev, err))
			} else {
				next.Upload = true
			}
		} else if old.Upload {
			deleteCake(dev, shapeUploadHandle)
			next.Upload = false
		}

		if target.download > 0 {
			if err := setCakeDownload(dev, target.download); err != nil {
				errs = append(errs, fmt.Sprintf("download on %s: %v", dev, err))
				if !old.Download {
					cleanupTrafficShape(dev, ShapeState{IFB: shapeIFB(dev), Download: true})
				}
			} else {
				next.IFB = shapeIFB(dev)
				next.Download = true
			}
		} else if old.Download {
			deleteCakeDownload(dev, old.IFB)
			next.Download = false
			next.IFB = ""
		}

		if len(errs) > 0 {
			warns = append(warns, "qos: "+strings.Join(errs, "; "))
		}
		if next.Upload || next.Download {
			env.St.TrafficShaping[dev] = next
		} else {
			delete(env.St.TrafficShaping, dev)
		}
	}
	return warns, nil
}

func setCake(dev, handle string, rateKbps int, ingress bool) error {
	if err := checkRootQdisc(dev, handle); err != nil {
		return err
	}
	args := []string{"qdisc", "replace", "dev", dev, "root", "handle", handle, "cake", "bandwidth", fmt.Sprintf("%dKbit", rateKbps), "diffserv4", "nat"}
	if ingress {
		args = append(args, "ingress")
	}
	_, err := run("tc", args...)
	return err
}

func checkRootQdisc(dev, ownedHandle string) error {
	out, err := run("tc", "qdisc", "show", "dev", dev)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[0] != "qdisc" || !strings.Contains(" "+line+" ", " root ") {
			continue
		}
		kind, handle := f[1], f[2]
		isDefault := handle == "0:" && (kind == "mq" || kind == "noqueue" || kind == "fq_codel" || kind == "fq" || kind == "pfifo_fast")
		if handle == ownedHandle || isDefault {
			return nil
		}
		return fmt.Errorf("device %s already has root qdisc %s (%s); refusing to replace it", dev, kind, handle)
	}
	return nil
}

func ensureIFB(dev string) (string, error) {
	name := shapeIFB(dev)
	if _, err := run("ip", "link", "show", "dev", name); err != nil {
		if _, addErr := run("ip", "link", "add", "dev", name, "type", "ifb"); addErr != nil {
			if _, showErr := run("ip", "link", "show", "dev", name); showErr != nil {
				return "", addErr
			}
		}
	}
	detail, err := run("ip", "-details", "link", "show", "dev", name)
	if err != nil {
		return "", err
	}
	if !strings.Contains(strings.ToLower(detail), "ifb") {
		return "", fmt.Errorf("device %s already exists and is not an IFB device", name)
	}
	if _, err := run("ip", "link", "set", "dev", name, "up"); err != nil {
		return "", err
	}
	return name, nil
}

func setCakeDownload(dev string, rateKbps int) error {
	if _, err := net.InterfaceByName(dev); err != nil {
		return fmt.Errorf("Linux device %s does not exist", dev)
	}
	ifb, err := ensureIFB(dev)
	if err != nil {
		return err
	}
	qdiscs, _ := run("tc", "qdisc", "show", "dev", dev)
	if !strings.Contains(qdiscs, "qdisc ingress ffff:") {
		if _, err := run("tc", "qdisc", "add", "dev", dev, "handle", "ffff:", "ingress"); err != nil {
			qdiscs, _ = run("tc", "qdisc", "show", "dev", dev)
			if !strings.Contains(qdiscs, "qdisc ingress ffff:") {
				return err
			}
		}
	}
	pref := shapeFilterPref(dev)
	filters, _ := run("tc", "filter", "show", "dev", dev, "parent", "ffff:")
	if strings.Contains(filters, "pref "+pref+" ") {
		if strings.Contains(filters, ifb) {
			return setCake(ifb, shapeDownloadHandle, rateKbps, true)
		}
		return fmt.Errorf("ingress filter priority %s on %s is already in use", pref, dev)
	}
	_, err = run("tc", "filter", "add", "dev", dev, "parent", "ffff:", "protocol", "all", "pref", pref, "handle", shapeFilterHandle(dev), "matchall", "action", "mirred", "egress", "redirect", "dev", ifb)
	if err != nil {
		return err
	}
	return setCake(ifb, shapeDownloadHandle, rateKbps, true)
}

func deleteCake(dev, handle string) {
	_, _ = run("tc", "qdisc", "del", "dev", dev, "root", "handle", handle)
}

func deleteCakeDownload(dev, ifb string) {
	_, _ = run("tc", "filter", "del", "dev", dev, "parent", "ffff:", "pref", shapeFilterPref(dev), "handle", shapeFilterHandle(dev))
	if ifb == "" {
		ifb = shapeIFB(dev)
	}
	deleteCake(ifb, shapeDownloadHandle)
	_, _ = run("ip", "link", "del", "dev", ifb)
}

func cleanupTrafficShape(dev string, st ShapeState) {
	if st.Upload {
		deleteCake(dev, shapeUploadHandle)
	}
	if st.Download {
		deleteCakeDownload(dev, st.IFB)
	}
}

func (s *trafficShapeSvc) Stop(env *Env) error {
	for dev, st := range env.St.TrafficShaping {
		cleanupTrafficShape(dev, st)
		delete(env.St.TrafficShaping, dev)
	}
	return nil
}

func (*trafficShapeSvc) Status(env *Env) string {
	if len(env.St.TrafficShaping) == 0 {
		return "idle"
	}
	return fmt.Sprintf("active on %d interface(s) (CAKE)", len(env.St.TrafficShaping))
}

func servicePolicyRate(p *config.ServicePolicy, dev string) (int, error) {
	v := p.BandwidthValue
	switch p.BandwidthUnit {
	case "kbps":
		return v, nil
	case "mbps":
		return v * 1000, nil
	case "gbps":
		return v * 1000000, nil
	case "percent":
		b, err := os.ReadFile("/sys/class/net/" + dev + "/speed")
		if err != nil {
			return 0, fmt.Errorf("cannot determine link speed for bandwidth percent")
		}
		var mbps int
		if _, err := fmt.Sscan(strings.TrimSpace(string(b)), &mbps); err != nil || mbps <= 0 {
			return 0, fmt.Errorf("link speed is unknown; use bandwidth kbps|mbps|gbps")
		}
		return mbps * 1000 * v / 100, nil
	default:
		return 0, fmt.Errorf("bandwidth is not configured")
	}
}
