package main

import "testing"

func TestResolveNodeListenAddrPreservesLegacyAdvertiseBinding(t *testing.T) {
	got, err := resolveNodeListenAddr("", "192.168.1.10:6000", "6000")
	if err != nil {
		t.Fatal(err)
	}
	if got != "192.168.1.10:6000" {
		t.Fatalf("listen address = %q, want legacy advertise binding", got)
	}
}

func TestResolveNodeListenAddrSeparatesContainerBinding(t *testing.T) {
	got, err := resolveNodeListenAddr("0.0.0.0:6000", "192.168.1.10:6000", "6000")
	if err != nil {
		t.Fatal(err)
	}
	if got != "0.0.0.0:6000" {
		t.Fatalf("listen address = %q, want container wildcard", got)
	}
}

func TestResolveNodeListenAddrRejectsAmbiguousOrUnsupportedValues(t *testing.T) {
	for _, value := range []string{
		"0.0.0.0:6001",
		"localhost:6000",
		"[::]:6000",
		"0.0.0.0",
		"0.0.0.0:0",
	} {
		if _, err := resolveNodeListenAddr(value, "192.168.1.10:6000", "6000"); err == nil {
			t.Errorf("resolveNodeListenAddr(%q) succeeded, want error", value)
		}
	}
}
