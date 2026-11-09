// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;
import {Test} from "forge-std/Test.sol";
import {SchnorrVault} from "../contracts/SchnorrVault.sol";
import {SchnorrVaultV3} from "../contracts/SchnorrVaultV3.sol";
import {FormalDualCustodyFixture} from "../formal-src/DualCustodyFixture.sol";

                                                                           
                                                                               
contract SchnorrVaultBatchSymbolicTest is Test, FormalDualCustodyFixture {
    function setUp() public { vault = _deploy(); vm.deal(address(vault), 10 ether); }
    receive() external payable {}

    function check_wrongSequenceDoesNotConsumeOrChangeAuthority(bytes32 id, uint96 gross, address recipient, uint64 delta) public {
        _assume(id, gross, recipient);
        SchnorrVault.WithdrawalBatch memory batch = _batch(1, 2 + uint256(delta), id, gross, recipient);
        _reject(batch);
    }
    function check_wrongDomainDoesNotConsume(bytes32 id, uint96 gross, address recipient) public {
        _assume(id, gross, recipient);
        SchnorrVault.WithdrawalBatch memory batch = _batch(1, 1, id, gross, recipient);
        batch.domain = bytes32(0); _reject(batch);
    }
    function check_wrongChainDoesNotConsume(bytes32 id, uint96 gross, address recipient) public {
        _assume(id, gross, recipient);
        SchnorrVault.WithdrawalBatch memory batch = _batch(1, 1, id, gross, recipient);
        batch.chainId = block.chainid + 1; _reject(batch);
    }
    function check_invalidGrossDoesNotConsume(bytes32 id, uint8 gross, address recipient) public {
        vm.assume(id != bytes32(0)); vm.assume(gross <= 1);
        vm.assume(recipient != address(0) && recipient != address(vault));
        _reject(_batch(1, 1, id, gross, recipient));
    }
    function check_duplicateRequestRollsBackWholeBatch(bytes32 id, uint96 gross, address recipient) public {
        _assume(id, gross, recipient);
        SchnorrVault.WithdrawalBatch memory batch = _batch(1, 1, id, gross, recipient);
        batch.items = new SchnorrVault.WithdrawalItem[](2);
        batch.items[0] = SchnorrVault.WithdrawalItem(id, address(0), recipient, gross, 0);
        batch.items[1] = SchnorrVault.WithdrawalItem(id, address(0), recipient, gross, 0);
        _reject(batch);
    }
    function check_unreferencedRewardBucketCannotAdvance(bytes32 id, uint96 gross, address recipient) public {
        _assume(id, gross, recipient);
        SchnorrVault.WithdrawalBatch memory batch = _batch(1, 1, id, gross, recipient);
        batch.rewardTokens = new address[](2); batch.rewardTokens[1] = address(1); _reject(batch);
    }
    function check_missingGuardianRollsBackHandover() public {
        SchnorrVaultV3.HandoverCertificate[] memory certs = _certificates(1, 2, 1);
        certs[0].guardianS = 0;
        (bool ok,) = address(vault).call(abi.encodeWithSelector(vault.catchUpAuthority.selector, certs));
        assert(!ok); assert(vault.authorityNonce() == 1); assert(vault.nextBatchSeq() == 1);
    }
    function check_missingOperatorRollsBackHandover() public {
        SchnorrVaultV3.HandoverCertificate[] memory certs = _certificates(1, 2, 1);
        certs[0].s = 0;
        (bool ok,) = address(vault).call(abi.encodeWithSelector(vault.catchUpAuthority.selector, certs));
        assert(!ok); assert(vault.authorityNonce() == 1); assert(vault.nextBatchSeq() == 1);
    }
    function check_pendingOldBatchCannotBeSkipped(uint64 extra) public {
        SchnorrVaultV3.HandoverCertificate[] memory certs = _certificates(1, 2, 2 + uint256(extra));
        (bool ok,) = address(vault).call(abi.encodeWithSelector(vault.catchUpAuthority.selector, certs));
        assert(!ok); assert(vault.authorityNonce() == 1); assert(vault.nextBatchSeq() == 1);
    }
    function check_invalidNewAuthorityBatchRollsBackHandover(bytes32 id, uint96 gross, address recipient) public {
        _assume(id, gross, recipient);
        SchnorrVault.WithdrawalBatch memory batch = _batch(2, 1, id, gross, recipient);
        SchnorrVaultV3.HandoverCertificate[] memory certs = _certificates(1, 2, 1);
        (bool ok,) = address(vault).call(abi.encodeWithSelector(vault.withdrawBatchWithHandovers.selector, batch,
            SchnorrVault.Signature(0,0,0), SchnorrVault.Signature(0,0,0), certs));
        assert(!ok); assert(vault.authorityNonce() == 1); assert(vault.nextBatchSeq() == 1);
        assert(vault.appliedCertificateDigest(2) == bytes32(0)); assert(!vault.consumed(id));
    }
    function _assume(bytes32 id, uint96 gross, address recipient) internal view {
        vm.assume(id != bytes32(0)); vm.assume(gross > 1);
        vm.assume(recipient != address(0) && recipient != address(vault));
    }
    function _reject(SchnorrVault.WithdrawalBatch memory batch) internal {
        (bool ok,) = address(vault).call(abi.encodeWithSelector(vault.withdrawBatch.selector, batch,
            SchnorrVault.Signature(0,0,0), SchnorrVault.Signature(0,0,0)));
        assert(!ok); assert(!vault.consumed(batch.items[0].requestId));
        assert(vault.authorityNonce() == 1); assert(vault.nextBatchSeq() == 1);
    }
}
