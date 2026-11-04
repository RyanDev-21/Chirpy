package catalog

import "testing"

func TestCatalog(t *testing.T) {
	if p, ok := LookupPlace("th-bangkok"); !ok || p.Country != "th" {
		t.Fatalf("bangkok = %+v %v", p, ok)
	}
	if p, ok := LookupPlace("mm"); !ok || p.Name != "Other city in Myanmar" {
		t.Fatalf("mm = %+v %v", p, ok)
	}
	if _, ok := LookupPlace("europe"); !ok {
		t.Fatal("europe missing")
	}
	if _, ok := LookupPlace("nowhere"); ok {
		t.Fatal("unknown place accepted")
	}
	seen := map[string]bool{}
	for _, c := range AllOptions().Countries {
		for _, p := range c.Places {
			if seen[p.ID] {
				t.Fatalf("duplicate place id %s", p.ID)
			}
			seen[p.ID] = true
		}
	}
	for _, i := range AllOptions().Interests {
		if seen["i:"+i.ID] || !ValidInterest(i.ID) {
			t.Fatalf("bad interest %s", i.ID)
		}
		seen["i:"+i.ID] = true
	}
}
