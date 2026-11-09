package consensus

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

                                                                                
                                                                                                   
                                                                                                                             
                                                                                          
  
                                                                                                     
                                                                                
  
                                                                                                
                                                                  
func TestConsensus_NoGlobalRandSeed_Regression(t *testing.T) {
	callPattern := regexp.MustCompile(`\brand\.Seed\s*\(`)

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read consensus dir: %v", err)
	}

	var offenders []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			                                                         
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") {
				continue
			}
			if callPattern.MatchString(line) {
				offenders = append(offenders, name+":"+strconv.Itoa(i+1))
			}
		}
	}

	if len(offenders) > 0 {
		t.Fatalf("consensus 生产代码禁止调用 rand.Seed，发现违规:\n  %s", strings.Join(offenders, "\n  "))
	}
}
