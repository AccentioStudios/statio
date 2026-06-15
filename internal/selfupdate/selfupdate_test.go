package selfupdate

import "testing"

func TestCompareAndOutdated(t *testing.T) {
	if Compare("v1.2.3", "v1.2.4") != -1 {
		t.Errorf("v1.2.3 should be < v1.2.4")
	}
	if Compare("1.2.3", "v1.2.3") != 0 {
		t.Errorf("missing-v should normalise to equal")
	}
	if Compare("v2.0.0", "v1.9.9") != 1 {
		t.Errorf("v2.0.0 should be > v1.9.9")
	}
	if !Outdated("v1.0.0", "v1.0.1") {
		t.Errorf("v1.0.0 is outdated vs v1.0.1")
	}
	if Outdated("v1.0.1", "v1.0.0") {
		t.Errorf("newer current is not outdated")
	}
	if Outdated("dev", "v1.0.0") {
		t.Errorf("dev build must never be reported outdated")
	}
	if Outdated("v1.0.0", "garbage") {
		t.Errorf("garbage latest must not trigger outdated")
	}
}

func TestIsVersion(t *testing.T) {
	for _, s := range []string{"v1.2.3", "1.2.3", "v0.0.1"} {
		if !IsVersion(s) {
			t.Errorf("IsVersion(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"dev", "", "latest", "abc"} {
		if IsVersion(s) {
			t.Errorf("IsVersion(%q) = true, want false", s)
		}
	}
}

func TestChecksumFor(t *testing.T) {
	sums := "aaa  statio_linux_amd64\nbbb  statio_linux_arm64\n"
	if got := checksumFor(sums, "statio_linux_arm64"); got != "bbb" {
		t.Errorf("checksumFor = %q, want bbb", got)
	}
	if got := checksumFor(sums, "statio_darwin_amd64"); got != "" {
		t.Errorf("checksumFor(missing) = %q, want empty", got)
	}
}
