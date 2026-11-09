package keys

import (
	"strings"
	"testing"
)

                                                                                                                 
                                                                
                                                                                                                          
                                                                                   
                                                                                                                                               
  
                                                                                                        

                                                                                                                                
                                                                                                                       
                              
  
                                       
                                                                                               
                                                    
                                                                                         
                                         
  
                                                                                                    
                                                                                                          
                                                                                     
func TestRegistry_NoDangerousStateOverKVOverlap(t *testing.T) {
	t.Parallel()

	for i, a := range prefixRegistry {
		for j, b := range prefixRegistry {
			if i == j || a.full == b.full {
				continue
			}
			                          
			if strings.HasPrefix(b.full, a.full) {
				if a.cat == CatState && b.cat == CatKV {
					t.Errorf("前缀歧义导致 fingerprint 污染风险："+
						"KV 前缀 %q 被 State 前缀 %q 吃掉；"+
						"应把 %q 改为 regExcluded 或挑一个不嵌套的 raw。",
						b.full, a.full, b.raw)
				}
			}
		}
	}
}

                                                                                     
                                                                       
func TestRegistry_NoRawDuplicateAcrossCategories(t *testing.T) {
	t.Parallel()

	seen := make(map[string]PrefixCategory)
	for _, r := range prefixRegistry {
		if prev, ok := seen[r.raw]; ok {
			if prev != r.cat {
				t.Errorf("raw 前缀 %q 被重复注册且 category 不同：prev=%d now=%d",
					r.raw, prev, r.cat)
			}
			                                                                                   
			continue
		}
		seen[r.raw] = r.cat
	}
}

                                                                                                  
                                                               
func TestCategorizeKey_UnknownKeyIsUnknown(t *testing.T) {
	t.Parallel()

	unknowns := []string{
		"",                                       
		"random_garbage_xyz",                     
		"v1_",                                          
		"v1_unknown_family_",                                    
		"v0_account_abc",                         
		"account_abc",                            
		"v1_block",                                                                                  
		strings.Repeat("x", 1024),             
	}

	for _, k := range unknowns {
		cat := CategorizeKey(k)
		if cat != CategoryUnknown {
			t.Errorf("未知 key %q 应分类为 CategoryUnknown，实得 %d", k, cat)
		}
		if IsStatefulKey(k) {
			t.Errorf("IsStatefulKey(%q) 必须返回 false（未知 key 不能污染 fingerprint）", k)
		}
	}
}

                                                                                           
                                                                                       
                                                                                                    
func TestCategorizeKey_ExcludedBeatsState(t *testing.T) {
	t.Parallel()

	                                                                                                     
	excluded := prefixesByCategory(CatExcluded)
	for _, ex := range excluded {
		probe := ex + "sample_suffix"
		if cat := CategorizeKey(probe); cat != CategoryKV {
			t.Errorf("CatExcluded 前缀 %q 下的 key 必须归 KV（被排除在 State 外），实得 %d",
				probe, cat)
		}
	}
}

                                                                                                         
                                                                                         
                                     
func TestRegistry_InitSlicesReady(t *testing.T) {
	t.Parallel()

	                                                                                      
	if len(statePrefixes) == 0 {
		t.Fatal("statePrefixes 为空：init 可能未执行或 regState 全被删了")
	}
	                                                                                  
	if excludeFromState == nil {
		t.Fatal("excludeFromState 必须在 init() 中赋值（可以为空切片，但不能是 nil）")
	}
	if kvPrefixes == nil {
		t.Fatal("kvPrefixes 必须在 init() 中赋值（可以为空切片，但不能是 nil）")
	}
}

                                                                                   
                                                                                      
func TestStateDBBusinessKeySpecs_MatchesRegistryState(t *testing.T) {
	t.Parallel()

	specs := StateDBBusinessKeySpecs()
	fromRegistry := map[string]string{}
	for _, r := range prefixRegistry {
		if r.cat == CatState {
			fromRegistry[r.full] = r.desc
		}
	}
	if len(specs) != len(fromRegistry) {
		t.Fatalf("StateDBBusinessKeySpecs len=%d, registry CatState len=%d",
			len(specs), len(fromRegistry))
	}
	for _, s := range specs {
		if _, ok := fromRegistry[s.Prefix]; !ok {
			t.Errorf("StateDBBusinessKeySpecs 包含未注册为 CatState 的前缀 %q", s.Prefix)
		}
	}
}
