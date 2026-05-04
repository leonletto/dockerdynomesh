package main

import "testing"

func TestValidateConfig(t *testing.T) {
	cases := []struct {
		name           string
		machine        string
		tailnet        string
		suffix         string
		wantErr        bool
	}{
		{"all valid", "host-mbp", "tail0123.ts.net", "docker.localhost", false},
		{"empty optional machine/tailnet", "", "", "docker.localhost", false},
		{"machine has command sub", "z$(rm -rf /)", "tail.ts.net", "docker.localhost", true},
		{"machine has backtick", "z`whoami`", "tail.ts.net", "docker.localhost", true},
		{"machine has space", "z mbp", "tail.ts.net", "docker.localhost", true},
		{"machine has slash", "z/mbp", "tail.ts.net", "docker.localhost", true},
		{"machine has semicolon", "z;ls", "tail.ts.net", "docker.localhost", true},
		{"tailnet trailing dot", "z", "tail.ts.net.", "docker.localhost", true},
		{"suffix empty", "z", "tail.ts.net", "", true},
		{"suffix with space", "z", "tail.ts.net", "orb localhost", true},
		{"suffix double-dot", "z", "tail.ts.net", "orb..localhost", true},
		{"suffix trailing hyphen", "z", "tail.ts.net", "orb-localhost-", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateConfig(tc.machine, tc.tailnet, tc.suffix)
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v; wantErr=%v", err, tc.wantErr)
			}
		})
	}
}
