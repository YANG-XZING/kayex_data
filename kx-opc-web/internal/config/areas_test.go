package config

import "testing"

func TestAreaOfIPv4Ranges(t *testing.T) {
	c := Config{Areas: []Area{{From: "172.30.3.61", To: "172.30.3.65", Area: "A"}, {From: "172.30.3.71", To: "172.30.3.130", Area: "B"}}}
	for ip, want := range map[string]string{"172.30.3.61": "A", "172.30.3.65": "A", "172.30.3.71": "B", "172.30.3.99": "B", "172.30.3.100": "B", "172.30.3.130": "B", "172.30.3.60": "", "172.30.3.66": "", "172.30.3.70": "", "172.30.3.131": "", "172.30.4.61": "", "not-an-ip": "", "172.30.3.256": ""} {
		if got := c.AreaOf(ip); got != want {
			t.Errorf("%s: got %q want %q", ip, got, want)
		}
	}
}

func TestAreaConfigurationValidation(t *testing.T) {
	c := defaults()
	c.TDengine.DSN = "test:local@ws(127.0.0.1:6041)/kayex_data"
	valid := []Area{{From: "172.30.3.61", To: "172.30.3.65", Area: "A"}, {From: "172.30.3.71", To: "172.30.3.130", Area: "B"}}
	c.Areas = valid
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	for _, areas := range [][]Area{
		{{From: "172.30.3.65", To: "172.30.3.61", Area: "A"}},
		{{From: "172.30.3.61", To: "", Area: "A"}},
		{{From: "172.30.3.61", To: "172.30.3.65", Area: ""}},
		{{From: "172.30.3.61", To: "172.30.3.71", Area: "A"}, {From: "172.30.3.71", To: "172.30.3.130", Area: "B"}},
	} {
		c.Areas = areas
		if c.validate() == nil {
			t.Errorf("accepted invalid ranges: %+v", areas)
		}
	}
}
