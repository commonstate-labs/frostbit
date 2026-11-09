package main

import "testing"

func TestNodeRegistrySnapshotsAreSortedByID(t *testing.T) {
	registry := NewNodeRegistry()
	for _, id := range []int{3, 1, 2, 0} {
		registry.Register(&NodeLauncher{
			config:   NodeConfig{ID: id},
			instance: &NodeInstance{ID: id, Address: "addr"},
		})
	}

	launchers := registry.All()
	instances := registry.AllInstances()
	for i := 0; i < 4; i++ {
		if launchers[i].config.ID != i {
			t.Fatalf("All()[%d].ID = %d, want %d", i, launchers[i].config.ID, i)
		}
		if instances[i].ID != i {
			t.Fatalf("AllInstances()[%d].ID = %d, want %d", i, instances[i].ID, i)
		}
	}
}
