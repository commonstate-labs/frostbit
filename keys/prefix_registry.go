package keys

                                                                       
type PrefixCategory int

const (
	CatKV       PrefixCategory = iota                                                        
	CatState                                                                              
	CatExcluded                                                                       
)

                                                               
type registeredPrefix struct {
	raw  string                           
	full string                              
	cat  PrefixCategory                                  
	desc string
}

                                                                            
                                                                           
                                                        
var prefixRegistry []registeredPrefix

func regPrefix(raw string, cat PrefixCategory, desc string) string {
	full := withVer(raw)
	prefixRegistry = append(prefixRegistry, registeredPrefix{
		raw: raw, full: full, cat: cat, desc: desc,
	})
	return full
}

                                                                                     
func regState(raw, desc string) string { return regPrefix(raw, CatState, desc) }

                                                                             
func regKV(raw, desc string) string { return regPrefix(raw, CatKV, desc) }

                                                                                       
func regExcluded(raw, desc string) string { return regPrefix(raw, CatExcluded, desc) }

                                                                                 
func prefixesByCategory(cat PrefixCategory) []string {
	var out []string
	for _, r := range prefixRegistry {
		if r.cat == cat {
			out = append(out, r.full)
		}
	}
	return out
}
