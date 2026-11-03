package cli

import (
	"strconv"

	"necorouter/internal/config"
)

// Optional parts of redistribute and default-information originate (manual, 20.4):
// [always] [metric N] [metric-type 1|2] [route-map NAME] — in that order, any word may be omitted.
//
// The engine passes the handler the values of all path arguments (v), not just its own, and calls
// the handler only at the leaf node. So each node's handler knows its own path
// (the list of keywords from the protocol to it) and parses v along that path.

// optCtx — where the parameters are written: for redistribute PROTOCOL or for default-information originate.
type optCtx struct {
	key       string // key in RedistMetric/RedistType/RedistRM
	isDefault bool
}

var optDefaultCtx = optCtx{key: config.DefaultKey, isDefault: true}

type optDef struct {
	kw, help string
}

var optDefs = []optDef{
	{"always", "Originate the default route even if the router has none"},
	{"metric", "Metric value"},
	{"metric-type", "OSPF metric type"},
	{"route-map", "Route map that selects and changes the routes"},
}

// applyOpt sets parameters along path; v — the path argument values in order.
func applyOpt(p *config.OSPF, c optCtx, path []string, v []string) {
	idx := 0
	next := func() string {
		if idx < len(v) {
			idx++
			return v[idx-1]
		}
		return ""
	}
	for _, kw := range path {
		switch kw {
		case "always":
			p.DefaultAlways = true
		case "metric":
			putInt(&p.RedistMetric, c.key, optAtoi(next()))
		case "metric-type":
			putInt(&p.RedistType, c.key, optAtoi(next()))
		case "route-map":
			name := next()
			if c.isDefault {
				p.DefaultRM = name
			} else {
				if p.RedistRM == nil {
					p.RedistRM = map[string]string{}
				}
				p.RedistRM[c.key] = name
			}
		}
	}
	if c.isDefault {
		p.DefaultOrig = true
	} else {
		addOnce(&p.Redistribute, c.key)
	}
}

// clearOpt removes one parameter (no metric / no route-map etc.).
func clearOpt(p *config.OSPF, c optCtx, kw string) {
	switch kw {
	case "always":
		p.DefaultAlways = false
	case "metric":
		delete(p.RedistMetric, c.key)
	case "metric-type":
		delete(p.RedistType, c.key)
	case "route-map":
		if c.isDefault {
			p.DefaultRM = ""
		} else {
			delete(p.RedistRM, c.key)
		}
	}
}

func putInt(m *map[string]int, k string, v int) {
	if *m == nil {
		*m = map[string]int{}
	}
	(*m)[k] = v
}

func optAtoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// optNodes builds the keyword variants after path prefix, starting at position i (order — optDefs).
func optNodes(o func(func(*config.OSPF, []string) error) Handler, c optCtx, prefix []string, i int) []*Node {
	var out []*Node
	for j := i; j < len(optDefs); j++ {
		d := optDefs[j]
		path := append(append([]string{}, prefix...), d.kw)
		kids := optNodes(o, c, path, j+1)
		run := o(func(p *config.OSPF, v []string) error { applyOpt(p, c, path, v); return nil })
		neg := o(func(p *config.OSPF, _ []string) error { clearOpt(p, c, d.kw); return nil })
		out = append(out, optKeyword(d, kids, run, neg))
	}
	return out
}

func optKeyword(d optDef, kids []*Node, run, neg Handler) *Node {
	switch d.kw {
	case "always":
		return K(d.kw, d.help, kids...).R(run).N(neg)
	case "metric":
		return K(d.kw, d.help, A(KInt, "VALUE", "Metric (0-16777214)", kids...).Rng(0, 16777214).R(run)).N(neg)
	case "metric-type":
		return K(d.kw, d.help, A(KInt, "TYPE", "1 or 2", kids...).Rng(1, 2).R(run)).N(neg)
	default: // route-map
		return K(d.kw, d.help, A(KWord, "NAME", "Route map name", kids...).Chk(nameOK).Cmp(routeMapNames).R(run)).N(neg)
	}
}
