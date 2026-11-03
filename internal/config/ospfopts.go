package config

import (
	"fmt"
	"strings"
)

// DefaultKey is the key in RedistMetric/RedistType for default-information originate.
const DefaultKey = "originate"

// optSuffix holds the optional parts of redistribute / default-information in the manual's order:
// metric, metric-type, route-map.
func (o *OSPF) optSuffix(key, rm string) string {
	var b strings.Builder
	if m, ok := o.RedistMetric[key]; ok {
		fmt.Fprintf(&b, " metric %d", m)
	}
	if t, ok := o.RedistType[key]; ok {
		fmt.Fprintf(&b, " metric-type %d", t)
	}
	if rm != "" {
		fmt.Fprintf(&b, " route-map %s", rm)
	}
	return b.String()
}

// RedistLine is the configuration line redistribute PROTOCOL [metric N] [metric-type T] [route-map NAME].
func (o *OSPF) RedistLine(proto string) string {
	return " redistribute " + proto + o.optSuffix(proto, o.RedistRM[proto])
}

// DefaultLine is the configuration line default-information originate [always] [metric N] [metric-type T] [route-map NAME].
func (o *OSPF) DefaultLine() string {
	s := " default-information originate"
	if o.DefaultAlways {
		s += " always"
	}
	return s + o.optSuffix(DefaultKey, o.DefaultRM)
}
