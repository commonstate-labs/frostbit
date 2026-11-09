// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import {SchnorrVault} from "../contracts/SchnorrVault.sol";
import {SchnorrExampleV3, SchnorrVaultV3} from "../contracts/SchnorrVaultV3.sol";

                                                                              
abstract contract FormalDualCustodyFixture {
    uint256 internal constant ORDER =
        21888242871839275222246405745257275088548364400416034343698204186575808495617;
    uint256 internal constant GUARDIAN_X =
        1368015179489954701390400359078579693043519447331113978918064868415326638035;
    uint256 internal constant GUARDIAN_Y =
        9918110051302171585080402603319702774565515993150576347155970296011118125764;
    SchnorrExampleV3 internal vault;

    function _deploy() internal returns (SchnorrExampleV3) {
        return new SchnorrExampleV3(1, 2, GUARDIAN_X, GUARDIAN_Y, 1, 51025);
    }

    function _op(bytes32 digest) internal pure returns (SchnorrVault.Signature memory) {
        return _signature(digest, 1, 1, 2);
    }

    function _guard(bytes32 digest) internal pure returns (SchnorrVault.Signature memory) {
        return _signature(digest, 2, GUARDIAN_X, GUARDIAN_Y);
    }

    function _signature(bytes32 digest, uint256 secret, uint256 px, uint256 py)
        private pure returns (SchnorrVault.Signature memory)
    {
        uint256 e = uint256(keccak256(abi.encodePacked(uint256(1), uint256(2), px, py, digest))) % ORDER;
        return SchnorrVault.Signature(1, 2, addmod(1, mulmod(e, secret, ORDER), ORDER));
    }

    function _certificates(uint256 from, uint256 to, uint256 cutoff)
        internal view returns (SchnorrVaultV3.HandoverCertificate[] memory certificates)
    {
        certificates = new SchnorrVaultV3.HandoverCertificate[](1);
        SchnorrVaultV3.HandoverCertificate memory c;
        c.fromEpoch = from; c.toEpoch = to; c.newPx = 1; c.newPy = 2;
        c.frozenNextBatchSeq = cutoff;
        bytes32 digest = vault.handoverDigest(c);
        SchnorrVault.Signature memory op = _op(digest);
        SchnorrVault.Signature memory guardian = _guard(digest);
        c.rx = op.rx; c.ry = op.ry; c.s = op.s;
        c.guardianRx = guardian.rx; c.guardianRy = guardian.ry; c.guardianS = guardian.s;
        certificates[0] = c;
    }

    function _batch(uint256 epoch, uint256 seq, bytes32 id, uint256 gross, address recipient)
        internal view returns (SchnorrVault.WithdrawalBatch memory)
    {
        address[] memory tokens = new address[](1);
        SchnorrVault.WithdrawalItem[] memory items = new SchnorrVault.WithdrawalItem[](1);
        items[0] = SchnorrVault.WithdrawalItem(id, address(0), recipient, gross, 0);
        return SchnorrVault.WithdrawalBatch(vault.WITHDRAW_DOMAIN(), block.chainid, address(vault), epoch, seq, tokens, items);
    }
}
