package tests

import (
	"os"
	"strings"
	"testing"
)

// TestRenderYamlPinsTheServiceRegion is the drift guard for the region's placement.
//
// The service and its database are named independently, so they can end up in different places. The
// service runs in Oregon; the database is a separate service with its own region, and a pair
// split across an ocean pays a round trip on every query.
//
// The region is written down rather than left to the platform's default, because the default is not
// visible anywhere in this repository -- and the dashboard and this file have diverged before. A
// region that is only known to Render is a region that cannot be checked.
func TestRenderYamlPinsTheServiceRegion(t *testing.T) {
	body, err := os.ReadFile("../render.yaml")
	if err != nil {
		body, err = os.ReadFile("render.yaml")
	}
	if err != nil {
		t.Fatalf("read render.yaml: %v", err)
	}

	manifest := string(body)

	if !strings.Contains(manifest, "region: oregon") {
		t.Errorf("render.yaml does not pin the service's region:\n%s", manifest)
	}
}
