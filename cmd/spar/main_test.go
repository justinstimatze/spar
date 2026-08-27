package main

import "testing"

func TestBuildVersionPrefersLdflagsOverride(t *testing.T) {
	orig := version
	defer func() { version = orig }()

	version = "v9.9.9"
	if got := buildVersion(); got != "v9.9.9" {
		t.Errorf("buildVersion() = %q, want the ldflags-baked v9.9.9 without touching debug.ReadBuildInfo", got)
	}
}
