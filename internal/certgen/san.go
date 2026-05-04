// Package certgen produces and serves wildcard certs for the dynomesh
// hostname pattern. SAN list is recomputed each time the discoverer
// reports a changed compose-project set.
package certgen

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// labelRE constrains values that get interpolated into SAN entries
// before being passed as command-line arguments to mkcert. Same shape
// as render.hostnameRE: forbid whitespace, backticks, commas, and any
// shell-significant punctuation an attacker could use to inject mkcert
// flags via untrusted compose-project labels.
var labelRE = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`)

// ValidateLabel reports whether s is a safe DNS-label-or-dotted-label
// value to interpolate into a SAN entry. Empty string is rejected.
// Beyond the regex, we additionally reject runs of dots, trailing dots,
// and trailing hyphens — all syntactically valid under the regex but
// produce malformed SAN entries that mkcert would reject at runtime.
func ValidateLabel(s string) bool {
	if !labelRE.MatchString(s) {
		return false
	}
	if strings.Contains(s, "..") || strings.HasSuffix(s, ".") || strings.HasSuffix(s, "-") {
		return false
	}
	return true
}

// SANs computes the SAN list for a multi-SAN wildcard cert covering:
//   - "*.<suffix>"                                  (per local suffix; standalone containers)
//   - "*.<project>.<suffix>"                        (per local suffix, per project)
//
// And, when machine and tailnet are both non-empty, the remote variants:
//   - "*.<machine>.<tailnet>"                       (standalone containers, remote)
//   - "*.<project>.<machine>.<tailnet>"             (one per project, remote)
//
// The local-resolution suffixes are intentionally OMITTED from the remote
// variants. Tailscale MagicDNS routes by machine name, so the suffix is
// only needed for local resolution. This also lets a project name encode
// a production-mirror domain (e.g. "demo.falconmode") and produce
// nginx.demo.falconmode.<machine>.<tailnet> for cross-machine access.
//
// Result is alphabetically sorted and deduplicated (deterministic for
// change detection). Duplicate project entries are collapsed in-function.
func SANs(suffixes []string, machine, tailnet string, projects []string) []string {
	// Dedupe projects while preserving deterministic output.
	seen := make(map[string]struct{}, len(projects))
	unique := projects[:0:0]
	for _, p := range projects {
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			unique = append(unique, p)
		}
	}
	projects = unique

	withTailnet := machine != "" && tailnet != ""
	out := make([]string, 0, len(suffixes)*(1+len(projects))+1+len(projects))
	for _, s := range suffixes {
		out = append(out, fmt.Sprintf("*.%s", s))
		for _, p := range projects {
			out = append(out, fmt.Sprintf("*.%s.%s", p, s))
		}
	}
	if withTailnet {
		out = append(out, fmt.Sprintf("*.%s.%s", machine, tailnet))
		for _, p := range projects {
			out = append(out, fmt.Sprintf("*.%s.%s.%s", p, machine, tailnet))
		}
	}
	sort.Strings(out)
	return out
}
