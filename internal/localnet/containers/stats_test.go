package containers

import "testing"

func TestParseSize(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"0B", 0},
		{"740MiB", 740 << 20},
		{"2GiB", 2 << 30},
		{"512kB", 512 << 10},
		{"1.5GiB", int64(1.5 * (1 << 30))},
		{"  256MiB ", 256 << 20},
		{"garbage", 0},
	}
	for _, c := range cases {
		if got := parseSize(c.in); got != c.want {
			t.Errorf("parseSize(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestParsePercent(t *testing.T) {
	if got := parsePercent("12.34%"); got != 12.34 {
		t.Errorf("parsePercent = %v, want 12.34", got)
	}
	if got := parsePercent("nope"); got != 0 {
		t.Errorf("parsePercent(bad) = %v, want 0", got)
	}
}

// TestParseStats pins the line-per-container docker stats decode, including
// skipping a malformed line without failing the whole sample.
func TestParseStats(t *testing.T) {
	raw := []byte(`{"Name":"demo-canton-1","CPUPerc":"12.50%","MemUsage":"740MiB / 2GiB"}
{"Name":"demo-postgres-1","CPUPerc":"3.00%","MemUsage":"128MiB / 2GiB"}
not-json-should-be-skipped
`)
	stats, err := parseStats(raw)
	if err != nil {
		t.Fatalf("parseStats: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("got %d stats, want 2 (bad line skipped)", len(stats))
	}
	c := stats["demo-canton-1"]
	if c.CPUPct != 12.5 || c.MemBytes != 740<<20 || c.MemLimit != 2<<30 {
		t.Errorf("demo-canton-1 = %+v", c)
	}
	if stats["demo-postgres-1"].MemBytes != 128<<20 {
		t.Errorf("postgres mem = %d", stats["demo-postgres-1"].MemBytes)
	}
}
