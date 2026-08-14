package awgconf

import (
	"strings"
	"testing"
)

func TestGenerateParamsRangesAndValidity(t *testing.T) {
	for i := 0; i < 100; i++ {
		p, err := GenerateParams()
		if err != nil {
			t.Fatalf("GenerateParams() error: %v", err)
		}
		if err := validateParams(p); err != nil {
			t.Fatalf("validateParams(%+v) error: %v", p, err)
		}
		if p.Jc == nil || *p.Jc < genJcMin || *p.Jc > genJcMax {
			t.Fatalf("jc = %v, want %d..%d", p.Jc, genJcMin, genJcMax)
		}
		if p.Jmin == nil || *p.Jmin < genJminMin || *p.Jmin > genJminMax {
			t.Fatalf("jmin = %v, want %d..%d", p.Jmin, genJminMin, genJminMax)
		}
		if p.Jmax == nil || *p.Jmax < *p.Jmin || *p.Jmax-*p.Jmin > genJmaxSpread {
			t.Fatalf("jmax = %v, want [jmin, jmin+%d] (jmin = %v)", p.Jmax, genJmaxSpread, p.Jmin)
		}
		if p.S1 == nil || *p.S1 < genSMin || *p.S1 > genSMax {
			t.Fatalf("s1 = %v, want %d..%d", p.S1, genSMin, genSMax)
		}
		if p.S2 == nil || *p.S2 < genSMin || *p.S2 > genSMax {
			t.Fatalf("s2 = %v, want %d..%d", p.S2, genSMin, genSMax)
		}
		if p.S3 != nil || p.S4 != nil {
			t.Fatalf("s3/s4 must stay unset, got %v/%v", p.S3, p.S4)
		}
		for name, h := range map[string]string{"h1": p.H1, "h2": p.H2, "h3": p.H3, "h4": p.H4} {
			if h == "" {
				t.Fatalf("%s: header not generated", name)
			}
			if err := validateHeaderSpec(h); err != nil {
				t.Fatalf("%s = %q: %v", name, h, err)
			}
		}
		for name, chain := range map[string]string{
			"i1": p.I1, "i2": p.I2, "i3": p.I3, "i4": p.I4, "i5": p.I5,
		} {
			if chain == "" {
				t.Fatalf("%s: obf chain not generated", name)
			}
			if err := validateObfChain(chain); err != nil {
				t.Fatalf("%s = %q: %v", name, chain, err)
			}
			if !strings.HasPrefix(chain, "<") {
				t.Fatalf("%s = %q: does not start with a tag", name, chain)
			}
		}
	}
}

func TestGenerateParamsRandomness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 20; i++ {
		p, err := GenerateParams()
		if err != nil {
			t.Fatalf("GenerateParams() error: %v", err)
		}
		raw, err := MarshalParams(p)
		if err != nil {
			t.Fatalf("MarshalParams() error: %v", err)
		}
		if seen[raw] {
			t.Fatalf("GenerateParams() produced a duplicate set: %s", raw)
		}
		seen[raw] = true
	}
}

func TestGenerateParamsCoversAllTagKinds(t *testing.T) {
	// The random chain generator must be able to produce every
	// permitted tag kind (b/t/r/rc/rd/d/ds/dz): drive it 500 times and
	// collect the tags seen in the five chains.
	tags := make(map[string]bool)
	for i := 0; i < 500; i++ {
		p, err := GenerateParams()
		if err != nil {
			t.Fatalf("GenerateParams() error: %v", err)
		}
		for _, chain := range []string{p.I1, p.I2, p.I3, p.I4, p.I5} {
			remaining := chain
			for {
				start := strings.IndexByte(remaining, '<')
				if start == -1 {
					break
				}
				relEnd := strings.IndexByte(remaining[start:], '>')
				if relEnd == -1 {
					t.Fatalf("chain %q missing >", chain)
				}
				fields := strings.Fields(remaining[start+1 : start+relEnd])
				if len(fields) > 0 {
					tags[fields[0]] = true
				}
				remaining = remaining[start+relEnd+1:]
			}
		}
	}
	for _, want := range genObfTags {
		if !tags[want] {
			t.Errorf("tag %q never produced in 500 generations", want)
		}
	}
}

func TestMarshalParamsCanonical(t *testing.T) {
	p := &Params{
		Jc: u16(3), Jmin: u16(1), Jmax: u16(5), S1: u16(1), S2: u16(2),
		H1: "3-5", I1: "<t><r 4><b 0x01>",
	}
	want := `{"jc":3,"jmin":1,"jmax":5,"s1":1,"s2":2,"h1":"3-5","i1":"<t><r 4><b 0x01>"}`
	got, err := MarshalParams(p)
	if err != nil {
		t.Fatalf("MarshalParams() error: %v", err)
	}
	if got != want {
		t.Fatalf("MarshalParams() = %s\nwant %s", got, want)
	}
}

func TestMarshalParamsEmpty(t *testing.T) {
	got, err := MarshalParams(&Params{})
	if err != nil {
		t.Fatalf("MarshalParams() error: %v", err)
	}
	if got != "{}" {
		t.Fatalf("MarshalParams(empty) = %q, want {}", got)
	}
}

func TestGenerateParamsRoundTrip(t *testing.T) {
	for i := 0; i < 10; i++ {
		p, err := GenerateParams()
		if err != nil {
			t.Fatalf("GenerateParams() error: %v", err)
		}
		raw, err := MarshalParams(p)
		if err != nil {
			t.Fatalf("MarshalParams() error: %v", err)
		}
		got, err := ParseParams(raw)
		if err != nil {
			t.Fatalf("ParseParams(MarshalParams(p)) error: %v", err)
		}
		if *got.Jc != *p.Jc || *got.Jmin != *p.Jmin || *got.Jmax != *p.Jmax ||
			*got.S1 != *p.S1 || *got.S2 != *p.S2 {
			t.Fatalf("numeric round trip mismatch: %+v vs %+v", got, p)
		}
		if got.H1 != p.H1 || got.H2 != p.H2 || got.H3 != p.H3 || got.H4 != p.H4 {
			t.Fatalf("header round trip mismatch: %+v vs %+v", got, p)
		}
		if got.I1 != p.I1 || got.I2 != p.I2 || got.I3 != p.I3 || got.I4 != p.I4 || got.I5 != p.I5 {
			t.Fatalf("obf chain round trip mismatch: %+v vs %+v", got, p)
		}
		if got.S3 != nil || got.S4 != nil {
			t.Fatalf("s3/s4 round trip must stay unset: %+v", got)
		}
	}
}
