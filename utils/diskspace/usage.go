package diskspace

import "fmt"

                                                                           
                                                                              
                                                                 
type Snapshot struct {
	Path        string
	TotalBytes  uint64
	FreeBytes   uint64
	FreePercent float64
}

                                                                            
                                       
func Usage(path string) (Snapshot, error) {
	if path == "" {
		path = "."
	}
	snapshot := Snapshot{Path: path}
	total, free, err := platformUsage(path)
	if err != nil {
		return snapshot, fmt.Errorf("disk usage for %q: %w", path, err)
	}
	if total == 0 {
		return snapshot, fmt.Errorf("disk usage for %q: total bytes is zero", path)
	}
	snapshot.TotalBytes = total
	snapshot.FreeBytes = free
	snapshot.FreePercent = float64(free) * 100 / float64(total)
	return snapshot, nil
}
