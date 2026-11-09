package planning

import (
	"dex/utils"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

func planningBTCGuardian() []byte {
	key, _ := btcec.PrivKeyFromBytes([]byte{31})
	return schnorr.SerializePubKey(key.PubKey())
}

func planningBTCOperator() []byte {
	key, _ := btcec.PrivKeyFromBytes([]byte{23})
	return schnorr.SerializePubKey(key.PubKey())
}

func planningBTCScript() []byte {
	script, _, _, _ := utils.BuildDualCustodyTaproot(planningBTCOperator(), planningBTCGuardian())
	return script
}
