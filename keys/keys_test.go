                    
package keys

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

                                               
func TestFrostKeys(t *testing.T) {
	                        
	t.Run("KeyFrostConfig", func(t *testing.T) {
		key := KeyFrostConfig()
		assert.Equal(t, "v1_frost_cfg", key)
	})

	                              
	t.Run("KeyFrostActiveMiners", func(t *testing.T) {
		key := KeyFrostActiveMiners()
		assert.Equal(t, "v1_frost_active_miners", key)
	})

	                             
	t.Run("KeyFrostVaultConfig", func(t *testing.T) {
		key := KeyFrostVaultConfig("BTC", 5)
		assert.Equal(t, "v1_frost_vault_cfg_BTC_5", key)

		key = KeyFrostVaultConfig("ETH", 100)
		assert.Equal(t, "v1_frost_vault_cfg_ETH_100", key)
	})

	                            
	t.Run("KeyFrostVaultState", func(t *testing.T) {
		key := KeyFrostVaultState("BTC", 3)
		assert.Equal(t, "v1_frost_vault_state_BTC_3", key)
	})

	                                 
	t.Run("KeyFrostVaultTransition", func(t *testing.T) {
		key := KeyFrostVaultTransition("BTC", 1, 12345)
		                                           
		assert.Equal(t, "v1_frost_vault_transition_BTC_1_00000000000000012345", key)
	})

	                                
	t.Run("KeyFrostVaultDkgCommit", func(t *testing.T) {
		key := KeyFrostVaultDkgCommit("ETH", 2, 999, "eth_2_999_1", "participant_addr")
		assert.Equal(t, "v1_frost_vault_dkg_commit_ETH_2_00000000000000000999_eth_2_999_1_participant_addr", key)
	})

	t.Run("KeyFrostVaultDkgCommitPrefix", func(t *testing.T) {
		key := KeyFrostVaultDkgCommitPrefix("ETH", 2, 999, "eth_2_999_1")
		assert.Equal(t, "v1_frost_vault_dkg_commit_ETH_2_00000000000000000999_eth_2_999_1_", key)
	})

	t.Run("KeyFrostVaultDkgSharePrefix", func(t *testing.T) {
		key := KeyFrostVaultDkgSharePrefix("BTC", 9, 11, "btc_9_11_3")
		assert.Equal(t, "v1_frost_vault_dkg_share_BTC_9_00000000000000000011_btc_9_11_3_", key)
	})

	                               
	t.Run("KeyFrostSignedPackage", func(t *testing.T) {
		key := KeyFrostSignedPackage("job-abc-123", 0)
		assert.Equal(t, "v1_frost_signed_pkg_job-abc-123_00000000000000000000", key)

		key = KeyFrostSignedPackage("job-xyz", 42)
		assert.Equal(t, "v1_frost_signed_pkg_job-xyz_00000000000000000042", key)
	})

	                                   
	t.Run("KeyFrostWithdraw", func(t *testing.T) {
		key := KeyFrostWithdraw("withdraw-id-123")
		assert.Equal(t, "v1_frost_withdraw_withdraw-id-123", key)
	})

	t.Run("KeyFrostWithdrawCompositePaid", func(t *testing.T) {
		key := KeyFrostWithdrawCompositePaid("withdraw-id-123")
		assert.Equal(t, "v1_frost_withdraw_paid_withdraw-id-123", key)
	})

	t.Run("KeyFrostWithdrawFIFOIndex", func(t *testing.T) {
		key := KeyFrostWithdrawFIFOIndex("BTC", "native", 100)
		assert.Equal(t, "v1_frost_withdraw_q_BTC_native_00000000000000000100", key)
	})

	t.Run("KeyFrostWithdrawFIFOSeq", func(t *testing.T) {
		key := KeyFrostWithdrawFIFOSeq("ETH", "USDT")
		assert.Equal(t, "v1_frost_withdraw_seq_ETH_USDT", key)
	})

	t.Run("KeyFrostWithdrawFIFOHead", func(t *testing.T) {
		key := KeyFrostWithdrawFIFOHead("TRX", "native")
		assert.Equal(t, "v1_frost_withdraw_head_TRX_native", key)
	})

	t.Run("KeyFrostWithdrawTxRef", func(t *testing.T) {
		key := KeyFrostWithdrawTxRef("tx-id-abc-123")
		assert.Equal(t, "v1_frost_withdraw_ref_tx-id-abc-123", key)
	})

	t.Run("AccountWithdrawReservationKeys", func(t *testing.T) {
		assert.Equal(t, "v1_frost_vault_reserved_eth_USDT_7", KeyFrostVaultReservedBalance("eth", "USDT", 7))
		assert.Equal(t, "v1_frost_funds_lot_remaining_eth_USDT_7_00000000000000000100_00000000000000000003", KeyFrostFundsLotRemaining("eth", "USDT", 7, 100, 3))
	})

	t.Run("KeyFrostLocalShare", func(t *testing.T) {
		key := KeyFrostLocalShare("btc", 4, 1)
		assert.Equal(t, "v1_local_frost_share_btc_4_00000000000000000001", key)
	})

	t.Run("KeyFrostLocalNonce", func(t *testing.T) {
		key := KeyFrostLocalNonce("aabbcc")
		assert.Equal(t, "v1_local_frost_nonce_aabbcc", key)
	})

	t.Run("KeyFrostLocalRoastSequence", func(t *testing.T) {
		key := KeyFrostLocalRoastSequence("aabbcc")
		assert.Equal(t, "v1_local_frost_roast_seq_aabbcc", key)
	})

	t.Run("KeyFrostVaultDkgComplaintPrefix", func(t *testing.T) {
		key := KeyFrostVaultDkgComplaintPrefix("BTC", 9, 11, "btc_9_11_1")
		assert.Equal(t, "v1_frost_vault_dkg_complaint_BTC_9_00000000000000000011_btc_9_11_1_", key)
	})

	t.Run("KeyFrostVaultDkgValidationAck", func(t *testing.T) {
		key := KeyFrostVaultDkgValidationAck("btc", 2, 7, "btc_2_7_1", "miner_addr")
		assert.Equal(t, "v1_frost_vault_dkg_validation_ack_btc_2_00000000000000000007_btc_2_7_1_miner_addr", key)
	})

	t.Run("KeyFrostVaultDkgValidationAckPrefix", func(t *testing.T) {
		key := KeyFrostVaultDkgValidationAckPrefix("btc", 2, 7, "btc_2_7_1")
		assert.Equal(t, "v1_frost_vault_dkg_validation_ack_btc_2_00000000000000000007_btc_2_7_1_", key)
	})
}
