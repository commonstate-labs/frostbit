package handlers

type RequestPayload struct {
	String1 string `json:"string1"`
	String2 string `json:"string2"`
}

                                                     
type HandshakePayload struct {
	ClientID  string `json:"client_id"`
	PublicKey string `json:"public_key"`                                
	Signature string `json:"signature"`                                                 
}

var ServerChallenge = "server_challenge_123456"
