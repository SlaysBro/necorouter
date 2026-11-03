package svc

import "testing"

func TestZoneFileStrict(t *testing.T) {
	for _, ok := range []string{"Europe/Moscow", "America/Argentina/Buenos_Aires", "Etc/GMT+3", "UTC", "utc+3", "GMT-5"} {
		if _, err := ZoneFile(ok); err != nil {
			t.Errorf("%q must be accepted: %v", ok, err)
		}
	}
	for _, bad := range []string{"../../etc/passwd", "/etc/passwd", "a/../b", "Europe/..", "x y", "a;b", "a\nb", "", "a/b/c/d", "."} {
		if _, err := ZoneFile(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}
