package config

import (
	"encoding/json"
	"testing"
)

func TestRangeAreasAndFurnaceNames(t *testing.T) {
	areas := []Area{{From: "172.30.3.61", To: "172.30.3.65", Area: "A"}, {From: "172.30.3.71", To: "172.30.3.130", Area: "B"}}
	raw, _ := json.Marshal(map[string]any{"port": 4840, "areas": areas, "furnaces": []string{"172.30.3.61-65", "172.30.3.71-130"}})
	fs, err := ParseFurnaces(raw, "opc_")
	if err != nil {
		t.Fatal(err)
	}
	if len(fs.Ranged) != 65 {
		t.Fatalf("got %d furnaces", len(fs.Ranged))
	}
	counts := map[byte]int{}
	for _, f := range fs.Ranged {
		counts[f.AreaDeviceID[0]]++
		if f.Port != 4840 {
			t.Fatal("port changed")
		}
	}
	if counts['A'] != 5 || counts['B'] != 60 {
		t.Fatalf("wrong counts: %v", counts)
	}
	for ip, want := range map[string]string{"172.30.3.61": "A061", "172.30.3.65": "A065", "172.30.3.71": "B071", "172.30.3.130": "B130", "172.30.3.60": "", "172.30.3.66": "", "172.30.3.70": "", "172.30.3.131": "", "172.30.4.61": ""} {
		if got := NewFurnace(ip, 4840, areas, "opc_").AreaDeviceID; got != want {
			t.Errorf("%s: got %q want %q", ip, got, want)
		}
	}
}

func TestInvalidAreaRangesRejected(t *testing.T) {
	for _, areas := range [][]Area{
		{{From: "172.30.3.65", To: "172.30.3.61", Area: "A"}},
		{{From: "172.30.3.256", To: "172.30.3.255", Area: "A"}},
		{{From: "172.30.3.61", To: "172.30.3.65", Area: ""}},
		{{From: "172.30.3.61", To: "172.30.3.71", Area: "A"}, {From: "172.30.3.71", To: "172.30.3.130", Area: "B"}},
	} {
		raw, _ := json.Marshal(map[string]any{"areas": areas, "furnaces": []string{"172.30.3.61"}})
		if _, err := ParseFurnaces(raw, "opc_"); err == nil {
			t.Errorf("accepted invalid ranges: %+v", areas)
		}
	}
	if _, err := ParseFurnaces([]byte(`{"areas":[{"prefix":"172.30.3.","area":"A"}],"furnaces":["172.30.3.61"]}`), "opc_"); err == nil {
		t.Fatal("obsolete prefix rule accepted")
	}
}
