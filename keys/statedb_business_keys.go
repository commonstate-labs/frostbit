package keys

                                                                             
                
type StateDBBusinessKeySpec struct {
	Prefix      string
	KeyBuilder  string
	Description string
}

                                                                                
                                                                            
                                                                              
                                  
func StateDBBusinessKeySpecs() []StateDBBusinessKeySpec {
	var out []StateDBBusinessKeySpec
	for _, r := range prefixRegistry {
		if r.cat == CatState {
			out = append(out, StateDBBusinessKeySpec{
				Prefix:      r.full,
				Description: r.desc,
			})
		}
	}
	return out
}

                                                                   
                                                                            
                                                     
var (
	statePrefixes    []string
	kvPrefixes       []string
	excludeFromState []string
)

func init() {
	statePrefixes = prefixesByCategory(CatState)
	kvPrefixes = prefixesByCategory(CatKV)
	excludeFromState = prefixesByCategory(CatExcluded)
}
