package app

import (
	"os"
	"strings"
	"testing"
)

// TestAppVersionMatchesNsgoYAML keeps the About row and User-Agent in step with
// the version the release workflow checks against the tag.
func TestAppVersionMatchesNsgoYAML(t *testing.T) {
	raw, err := os.ReadFile("../../nsgo.yaml")
	if err != nil {
		t.Fatal(err)
	}
	version := ""
	for _, line := range strings.Split(string(raw), "\n") {
		if rest, ok := strings.CutPrefix(line, "version:"); ok {
			version = strings.Trim(strings.TrimSpace(rest), `"'`)
			break
		}
	}
	if version == "" {
		t.Fatal("nsgo.yaml has no top-level version")
	}
	if version != appVersion {
		t.Fatalf("nsgo.yaml says version %q, appVersion is %q", version, appVersion)
	}
}
