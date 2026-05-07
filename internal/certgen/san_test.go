package certgen

import (
	"reflect"
	"testing"
)

func TestSANsLocalOnly(t *testing.T) {
	got := SANs([]string{"docker.localhost"}, "", "", []string{"repo", "falcon-demo"})
	want := []string{
		"*.docker.localhost",
		"*.falcon-demo.docker.localhost",
		"*.repo.docker.localhost",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("\ngot:  %v\nwant: %v", got, want)
	}
}

func TestSANsWithTailnet(t *testing.T) {
	got := SANs([]string{"docker.localhost"}, "host-mbp", "tail0123.ts.net", []string{"repo"})
	want := []string{
		"*.docker.localhost",
		"*.host-mbp.tail0123.ts.net",
		"*.repo.docker.localhost",
		"*.repo.host-mbp.tail0123.ts.net",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("\ngot:  %v\nwant: %v", got, want)
	}
}

func TestSANsCustomDomainProject(t *testing.T) {
	got := SANs([]string{"localhost"}, "host-mbp", "tail0123.ts.net", []string{"demo.falconmode"})
	want := []string{
		"*.demo.falconmode.host-mbp.tail0123.ts.net",
		"*.demo.falconmode.localhost",
		"*.host-mbp.tail0123.ts.net",
		"*.localhost",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("\ngot:  %v\nwant: %v", got, want)
	}
}

func TestValidateLabelRejected(t *testing.T) {
	bad := []string{"", "UPPER", "-leading", "a.", "a-", "a..b", "..", "with space", "back`tick"}
	for _, s := range bad {
		if ValidateLabel(s) {
			t.Errorf("ValidateLabel(%q) = true, want false", s)
		}
	}
}

func TestValidateLabelAccepted(t *testing.T) {
	good := []string{"a", "docker.localhost", "host-mbp", "tail0123.ts.net", "demo.falconmode", "repo"}
	for _, s := range good {
		if !ValidateLabel(s) {
			t.Errorf("ValidateLabel(%q) = false, want true", s)
		}
	}
}

func TestSANsEmptyProjects(t *testing.T) {
	got := SANs([]string{"docker.localhost"}, "", "", nil)
	want := []string{"*.docker.localhost"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("\ngot:  %v\nwant: %v", got, want)
	}
}

func TestSANsMultipleSuffixes(t *testing.T) {
	got := SANs(
		[]string{"docker.localhost", "orb.local"},
		"", "",
		[]string{"repo"},
	)
	want := []string{
		"*.docker.localhost",
		"*.orb.local",
		"*.repo.docker.localhost",
		"*.repo.orb.local",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SANs mismatch:\n got=%v\nwant=%v", got, want)
	}
}

func TestSANsDuplicateProjectsCollapsed(t *testing.T) {
	// Passing duplicate project names must not inflate the SAN list.
	got := SANs([]string{"docker.localhost"}, "", "", []string{"repo", "repo", "falcon-demo", "repo"})
	want := []string{
		"*.docker.localhost",
		"*.falcon-demo.docker.localhost",
		"*.repo.docker.localhost",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("\ngot:  %v\nwant: %v", got, want)
	}
}

func TestSANsMultipleSuffixesWithTailnet(t *testing.T) {
	got := SANs(
		[]string{"docker.localhost", "orb.local"},
		"host-mbp", "tail0123.ts.net",
		[]string{"repo"},
	)
	want := []string{
		"*.docker.localhost",
		"*.host-mbp.tail0123.ts.net",
		"*.orb.local",
		"*.repo.docker.localhost",
		"*.repo.host-mbp.tail0123.ts.net",
		"*.repo.orb.local",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SANs mismatch:\n got=%v\nwant=%v", got, want)
	}
}

func TestSANs_DottedProjectName(t *testing.T) {
	got := SANs(
		[]string{"localhost"},
		"host-mbp", "tail0123.ts.net",
		[]string{"demo.falconmode"},
	)
	want := []string{
		"*.demo.falconmode.host-mbp.tail0123.ts.net",
		"*.demo.falconmode.localhost",
		"*.host-mbp.tail0123.ts.net",
		"*.localhost",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SANs mismatch\n got: %v\nwant: %v", got, want)
	}
}
