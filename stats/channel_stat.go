package stats

                                       
type ChannelStat struct {
	Name   string  `json:"name"`                    
	Module string  `json:"module"`                
	Len    int     `json:"len"`                   
	Cap    int     `json:"cap"`             
	Usage  float64 `json:"usage"`                        
}

                                          
func NewChannelStat(name, module string, length, capacity int) ChannelStat {
	usage := 0.0
	if capacity > 0 {
		usage = float64(length) / float64(capacity)
	}
	return ChannelStat{
		Name:   name,
		Module: module,
		Len:    length,
		Cap:    capacity,
		Usage:  usage,
	}
}
