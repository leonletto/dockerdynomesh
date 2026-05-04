package main

import (
	"reflect"
	"testing"
)

func TestParseTakeoverSuffixes(t *testing.T) {
	cases := []struct {
		name    string
		primary string
		env     string
		want    []string
		wantErr bool
	}{
		{"empty env", "docker.localhost", "", []string{"docker.localhost"}, false},
		{"single takeover", "docker.localhost", "orb.local", []string{"docker.localhost", "orb.local"}, false},
		{"multiple, with whitespace", "docker.localhost", " orb.local , colima.local ", []string{"colima.local", "docker.localhost", "orb.local"}, false},
		{"dedup with primary", "docker.localhost", "docker.localhost,orb.local", []string{"docker.localhost", "orb.local"}, false},
		{"invalid label", "docker.localhost", "BAD..NAME", nil, true},
		{"empty entry", "docker.localhost", "orb.local,,colima.local", []string{"colima.local", "docker.localhost", "orb.local"}, false},
		{"invalid primary", "BAD..NAME", "", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTakeoverSuffixes(tc.primary, tc.env)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if err != nil {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
