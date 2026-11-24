package api

import (
	"testing"
	"time"
)

func TestUnixTreatsZeroTimeAsNone(t *testing.T) {
	if unix(time.Time{}) != 0 || unixPtr(nil) != 0 {
		t.Fatal("zero time must map to 0")
	}
	now := time.Unix(1700000000, 0)
	if unix(now) != 1700000000 || unixPtr(&now) != 1700000000 {
		t.Fatal("unix mismatch")
	}
}

func TestIDsRejectWrapAndZero(t *testing.T) {
	for _, v := range []int64{0, -1} {
		if _, err := id(v); err == nil {
			t.Errorf("id(%d) must fail", v)
		}
	}
	if got, err := id(7); err != nil || got != 7 {
		t.Fatalf("id(7) = %d, %v", got, err)
	}
	if _, err := optID(-1); err == nil {
		t.Error("optID(-1) must fail")
	}
	if got, err := optID(0); err != nil || got != 0 {
		t.Errorf("optID(0) = %d, %v", got, err)
	}
}

func TestParseIDs(t *testing.T) {
	if got, err := parseIDs([]string{"1", "22"}, 5); err != nil || len(got) != 2 || got[1] != 22 {
		t.Fatalf("parseIDs = %v, %v", got, err)
	}
	for name, in := range map[string][]string{
		"not a number": {"x"},
		"zero":         {"0"},
		"negative":     {"-3"},
		"too big":      {"99999999999"},
		"too many":     {"1", "2", "3", "4", "5", "6"},
	} {
		if _, err := parseIDs(in, 5); err == nil {
			t.Errorf("%s: must fail", name)
		}
	}
}
