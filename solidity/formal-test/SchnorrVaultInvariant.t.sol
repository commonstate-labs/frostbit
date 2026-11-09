// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import {Test} from "forge-std/Test.sol";
import {StdInvariant} from "forge-std/StdInvariant.sol";
import {SchnorrVault} from "../contracts/SchnorrVault.sol";
import {SchnorrExampleV3, SchnorrVaultV3} from "../contracts/SchnorrVaultV3.sol";
import {FormalDualCustodyFixture} from "../formal-src/DualCustodyFixture.sol";

contract FormalRejectingNativeReceiver {
    receive() external payable { revert("reject native transfer"); }
}

contract SchnorrVaultBatchHandler is FormalDualCustodyFixture {
    FormalRejectingNativeReceiver public immutable rejectingReceiver;
    uint256 public immutable initialVaultBalance;
    uint256 public expectedEpoch = 1;
    uint256 public expectedBatchSeq = 1;
    uint256 public successfulNet;
    uint256 public totalFees;
    uint256 public rewardsPaid;
    uint256 public requestSequence;
    uint256 public handoverApplications;
    bytes32 internal lastId;
    uint256 internal lastEpoch;
    uint256 internal lastSeq;
    uint256 internal lastGross;
    address internal lastRecipient;
    bool public duplicateSpendObserved;
    bool public invalidBatchAccepted;
    bool public failedRewardClaimLostCredit;
    bool public standaloneAuthorityChangeAccepted;
    uint256 public immutable guardianX;
    uint256 public immutable guardianY;

    constructor(SchnorrExampleV3 target) {
        vault = target;
        rejectingReceiver = new FormalRejectingNativeReceiver();
        initialVaultBalance = address(target).balance;
        (guardianX, guardianY) = target.G();
    }
    receive() external payable {}

    function executeFresh(uint96 rawGross, bool rejectPayment) external {
        uint256 gross = _boundedGross(rawGross);
        if (gross == 0) return;
        address recipient = rejectPayment ? address(rejectingReceiver) : address(this);
        SchnorrVault.WithdrawalBatch memory batch = _batch(expectedEpoch, expectedBatchSeq, _nextId(), gross, recipient);
        bytes32 digest = keccak256(abi.encode(batch));
        vault.withdrawBatch(batch, _op(digest), _guard(digest));
        _accepted(batch, rejectPayment);
    }

    function executeHandoverFresh(uint96 rawGross, bool rejectPayment) external {
        uint256 gross = _boundedGross(rawGross);
        if (gross == 0 || expectedEpoch == type(uint64).max) return;
        address recipient = rejectPayment ? address(rejectingReceiver) : address(this);
        SchnorrVault.WithdrawalBatch memory batch = _batch(expectedEpoch + 1, expectedBatchSeq, _nextId(), gross, recipient);
        bytes32 digest = keccak256(abi.encode(batch));
        vault.withdrawBatchWithHandovers(batch, _op(digest), _guard(digest), _certificates(expectedEpoch, expectedEpoch + 1, expectedBatchSeq));
        expectedEpoch++; handoverApplications++;
        _accepted(batch, rejectPayment);
    }

    function replayLatest() external {
        if (lastId == bytes32(0)) return;
        SchnorrVault.WithdrawalBatch memory batch = _batch(lastEpoch, lastSeq, lastId, lastGross, lastRecipient);
        bytes32 digest = keccak256(abi.encode(batch));
        try vault.withdrawBatch(batch, _op(digest), _guard(digest)) { duplicateSpendObserved = true; } catch {}
    }

    function invalidOrSingleSignatureCannotConsume(uint8 mode) external {
        SchnorrVault.WithdrawalBatch memory batch = _batch(expectedEpoch, expectedBatchSeq, _nextId(), 2, address(this));
        if (mode % 4 == 0) batch.batchSeq++;
        if (mode % 4 == 1) batch.domain = bytes32(0);
        bytes32 digest = keccak256(abi.encode(batch));
        SchnorrVault.Signature memory op = mode % 4 == 2 ? SchnorrVault.Signature(0, 0, 0) : _op(digest);
        SchnorrVault.Signature memory guard = mode % 4 == 3 ? SchnorrVault.Signature(0, 0, 0) : _guard(digest);
        try vault.withdrawBatch(batch, op, guard) { invalidBatchAccepted = true; } catch {
            if (vault.consumed(batch.items[0].requestId)) invalidBatchAccepted = true;
        }
    }

    function singlePartyCannotRotate(bool removeGuardian) external {
        SchnorrVaultV3.HandoverCertificate[] memory certs = _certificates(expectedEpoch, expectedEpoch + 1, expectedBatchSeq);
        if (removeGuardian) certs[0].guardianS = 0; else certs[0].s = 0;
        try vault.catchUpAuthority(certs) { invalidBatchAccepted = true; } catch {}
    }

    function claimReward() external {
        uint256 amount = vault.claimableReward(address(0), address(this));
        if (amount == 0) return;
        vault.claimReward(address(0), address(this)); rewardsPaid += amount;
    }
    function failRewardClaim() external {
        uint256 credit = vault.claimableReward(address(0), address(this));
        if (credit == 0) return;
        try vault.claimReward(address(0), address(rejectingReceiver)) { invalidBatchAccepted = true; } catch {
            if (vault.claimableReward(address(0), address(this)) != credit) failedRewardClaimLostCredit = true;
        }
    }
    function standaloneChangePubCannotExist() external {
        (bool ok,) = address(vault).call(abi.encodeWithSignature("changePub(uint256,uint256,uint256,uint256,uint256,uint256)", 1, 2, expectedEpoch + 1, 1, 2, 1));
        if (ok) standaloneAuthorityChangeAccepted = true;
    }
    function _boundedGross(uint96 rawGross) internal view returns (uint256) {
        uint256 outstanding = totalFees - rewardsPaid;
        uint256 balance = address(vault).balance;
        if (balance <= outstanding + 1) return 0;
        uint256 ceiling = balance - outstanding;
        if (ceiling > 1 ether) ceiling = 1 ether;
        return 2 + uint256(rawGross) % (ceiling - 1);
    }
    function _accepted(SchnorrVault.WithdrawalBatch memory batch, bool rejected) internal {
        SchnorrVault.WithdrawalItem memory item = batch.items[0];
        uint256 fee = item.grossAmount / 10000 + (item.grossAmount % 10000 == 0 ? 0 : 1);
        if (!vault.consumed(item.requestId)) invalidBatchAccepted = true;
        if (!rejected) successfulNet += item.grossAmount - fee;
        totalFees += fee; expectedBatchSeq++;
        lastId = item.requestId; lastEpoch = batch.epoch; lastSeq = batch.batchSeq;
        lastGross = item.grossAmount; lastRecipient = item.to;
    }
    function _nextId() internal returns (bytes32) { return keccak256(abi.encode("DEX_FORMAL_DUAL", ++requestSequence)); }
}

contract SchnorrVaultBatchInvariantTest is StdInvariant, Test, FormalDualCustodyFixture {
    SchnorrVaultBatchHandler internal handler;
    function setUp() public {
        vault = _deploy(); vm.deal(address(vault), 100 ether);
        handler = new SchnorrVaultBatchHandler(vault); targetContract(address(handler));
    }
    function invariant_nativeCustodyConservedAcrossFailedAttemptsAndRewards() public view {
        assertEq(address(vault).balance + handler.successfulNet() + handler.rewardsPaid(), handler.initialVaultBalance());
    }
    function invariant_rewardCreditEqualsAcceptedFeesMinusClaims() public view {
        assertEq(vault.claimableReward(address(0), address(handler)), handler.totalFees() - handler.rewardsPaid());
    }
    function invariant_dualAuthoritySequenceAndGuardian() public view {
        assertEq(vault.authorityNonce(), handler.expectedEpoch());
        assertEq(vault.authorityNonce(), 1 + handler.handoverApplications());
        assertEq(vault.nextBatchSeq(), handler.expectedBatchSeq());
        (uint256 gx, uint256 gy) = vault.G();
        assertEq(gx, handler.guardianX()); assertEq(gy, handler.guardianY());
    }
    function invariant_replayInvalidInputAndClaimFailureAreStateSafe() public view {
        assertFalse(handler.duplicateSpendObserved()); assertFalse(handler.invalidBatchAccepted());
        assertFalse(handler.failedRewardClaimLostCredit()); assertFalse(handler.standaloneAuthorityChangeAccepted());
    }
}

contract SchnorrVaultBatchFormalUnitTest is Test, FormalDualCustodyFixture {
    FormalRejectingNativeReceiver internal rejecting;
    receive() external payable {}
    function setUp() public { vault = _deploy(); rejecting = new FormalRejectingNativeReceiver(); vm.deal(address(vault), 10 ether); }

    function test_mixedFailureConsumesEveryItemAdvancesSequenceAndPaysLaterItem() public {
        SchnorrVault.WithdrawalBatch memory batch = _batch(1, 1, keccak256("failed"), 30001, address(rejecting));
        batch.items = new SchnorrVault.WithdrawalItem[](2);
        batch.items[0] = SchnorrVault.WithdrawalItem(keccak256("failed"), address(0), address(rejecting), 30001, 0);
        batch.items[1] = SchnorrVault.WithdrawalItem(keccak256("paid"), address(0), address(this), 40001, 0);
        uint256 beforeBalance = address(this).balance;
        bytes32 digest = keccak256(abi.encode(batch));
        SchnorrVault.Signature memory guardian = _guard(digest);
        vault.withdrawBatch(batch, _op(digest), guardian);
        assertTrue(vault.consumed(batch.items[0].requestId)); assertTrue(vault.consumed(batch.items[1].requestId));
        assertEq(address(this).balance - beforeBalance, 39996);
        assertEq(vault.claimableReward(address(0), address(this)), 9); assertEq(vault.nextBatchSeq(), 2);
        vm.expectRevert("Vault: wrong batch sequence"); vault.withdrawBatch(batch, _op(digest), guardian);
    }

    function test_handoverDualSignedReplayAndConflict() public {
        SchnorrVaultV3.HandoverCertificate[] memory certs = _certificates(1, 2, 1);
        vault.catchUpAuthority(certs); vault.catchUpAuthority(certs);
        assertEq(vault.authorityNonce(), 2); assertEq(vault.nextBatchSeq(), 1);
        certs[0].frozenNextBatchSeq = 2;
        vm.expectRevert("Vault: conflicting certificate"); vault.catchUpAuthority(certs);
    }

    function test_pendingOldBatchCannotBeSkipped() public {
        SchnorrVaultV3.HandoverCertificate[] memory certs = _certificates(1, 2, 2);
        vm.expectRevert("Vault: pending old batches"); vault.catchUpAuthority(certs);
        assertEq(vault.authorityNonce(), 1); assertEq(vault.nextBatchSeq(), 1);
    }

    function test_bothRolesRequiredForWithdrawAndRotation() public {
        SchnorrVault.WithdrawalBatch memory batch = _batch(1, 1, keccak256("single"), 2, address(this));
        bytes32 digest = keccak256(abi.encode(batch));
        SchnorrVault.Signature memory guardian = _guard(digest);
        vm.expectRevert("Vault: invalid guardian signature"); vault.withdrawBatch(batch, _op(digest), SchnorrVault.Signature(0,0,0));
        vm.expectRevert("Vault: invalid signature"); vault.withdrawBatch(batch, SchnorrVault.Signature(0,0,0), guardian);
        SchnorrVaultV3.HandoverCertificate[] memory certs = _certificates(1, 2, 1);
        certs[0].guardianS = 0;
        vm.expectRevert("Vault: invalid guardian pubupdate signature"); vault.catchUpAuthority(certs);
        assertFalse(vault.consumed(batch.items[0].requestId)); assertEq(vault.authorityNonce(), 1); assertEq(vault.nextBatchSeq(), 1);
    }

    function test_failedPaymentStillCommitsDualHandoverAndSequence() public {
        SchnorrVault.WithdrawalBatch memory batch = _batch(2, 1, keccak256("handover-failed"), 10001, address(rejecting));
        bytes32 digest = keccak256(abi.encode(batch));
        vault.withdrawBatchWithHandovers(batch, _op(digest), _guard(digest), _certificates(1, 2, 1));
        assertEq(vault.authorityNonce(), 2); assertEq(vault.nextBatchSeq(), 2); assertTrue(vault.consumed(batch.items[0].requestId));
    }
    function test_invalidNewBatchRollsBackAlreadyValidatedCertificates() public {
        SchnorrVault.WithdrawalBatch memory batch = _batch(2, 1, keccak256("invalid-new-batch"), 10001, address(this));
        SchnorrVaultV3.HandoverCertificate[] memory certs = _certificates(1, 2, 1);
        SchnorrVault.Signature memory op = _op(keccak256(abi.encode(batch)));
        vm.expectRevert("Vault: invalid guardian signature");
        vault.withdrawBatchWithHandovers(batch, op, SchnorrVault.Signature(0,0,0), certs);
        assertEq(vault.authorityNonce(), 1); assertEq(vault.nextBatchSeq(), 1);
        assertEq(vault.appliedCertificateDigest(2), bytes32(0)); assertFalse(vault.consumed(batch.items[0].requestId));
    }
}
