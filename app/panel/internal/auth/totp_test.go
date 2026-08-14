package auth

import (
	"testing"
	"time"
)

func TestTOTPRFC6238Vectors(t *testing.T) {
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	cases := []struct {
		unix int64
		want string
	}{
		{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"},
		{1234567890, "005924"}, {2000000000, "279037"},
	}
	for _, tc := range cases {
		got, err := TOTPCode(secret, time.Unix(tc.unix, 0))
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("at %d: got %s want %s", tc.unix, got, tc.want)
		}
	}
}

func TestTOTPWindow(t *testing.T) {
	secret := "JBSWY3DPEHPK3PXP"
	at := time.Unix(1700000000, 0)
	code, err := TOTPCode(secret, at.Add(-30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyTOTP(secret, code, at) {
		t.Fatal("previous period must be accepted")
	}
	if VerifyTOTP(secret, code, at.Add(2*time.Minute)) {
		t.Fatal("stale code must be rejected")
	}
}
