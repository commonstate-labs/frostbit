// SPDX-License-Identifier: MIT
pragma solidity >=0.8.18 <0.9.0;
import "./SchnorrVault.sol";

                                                                                   
                                                                                   
contract SchnorrVaultV3 is SchnorrVault {
    uint256 public constant AUTHORITY_PROTOCOL = 3;
    uint256 public constant MAX_HANDOVERS_PER_CALL = 8;
    bytes32 public constant HANDOVER_DOMAIN = keccak256("DEX_VAULT_AUTHORITY_HANDOVER_V3");
    mapping(uint256 => bytes32) public appliedCertificateDigest;

    struct HandoverCertificate {
        uint256 fromEpoch;
        uint256 toEpoch;
        uint256 newPx;
        uint256 newPy;
        uint256 frozenNextBatchSeq;
        uint256 rx;
        uint256 ry;
        uint256 s;
        uint256 guardianRx;
        uint256 guardianRy;
        uint256 guardianS;
    }

    constructor(uint256 px, uint256 py, uint256 gx, uint256 gy, uint256 initialEpoch, uint256 dexChainId_)
        SchnorrVault(px, py, gx, gy, initialEpoch, dexChainId_)
    {
        require(initialEpoch != 0 && initialEpoch <= type(uint64).max, "Vault: invalid epoch");
    }

    function withdrawBatchWithHandovers(
        WithdrawalBatch calldata batch,
        Signature calldata signature,
        Signature calldata guardianSignature,
        HandoverCertificate[] calldata certificates
    ) external nonReentrant {
        _applyCertificates(certificates);
        (bytes32 digest, uint256[] memory rewards) = _validateBatch(batch);
        require(batch.epoch == authorityNonce, "Vault: wrong epoch");
        require(verifySignature(P.x, P.y, signature.rx, signature.ry, signature.s, digest),
            "Vault: invalid signature");
        require(verifySignature(G.x, G.y, guardianSignature.rx, guardianSignature.ry, guardianSignature.s, digest),
            "Vault: invalid guardian signature");
        _consumeAccrueAndExecute(batch, digest, rewards);
    }

                                                                                     
                                                                           
    function catchUpAuthority(HandoverCertificate[] calldata certificates) external nonReentrant {
        require(certificates.length != 0, "Vault: empty handovers");
        _applyCertificates(certificates);
    }

    function handoverDigest(HandoverCertificate calldata c) public view returns (bytes32) {
        return keccak256(abi.encode(HANDOVER_DOMAIN, dexChainId, block.chainid, address(this),
            c.fromEpoch, c.toEpoch, c.newPx, c.newPy, c.frozenNextBatchSeq));
    }

    function _applyCertificates(HandoverCertificate[] calldata certificates) private {
        require(certificates.length <= MAX_HANDOVERS_PER_CALL, "Vault: too many handovers");
        for (uint256 i; i < certificates.length; ++i) {
            HandoverCertificate calldata c = certificates[i];
            require(c.fromEpoch != 0 && c.fromEpoch < type(uint64).max && c.toEpoch == c.fromEpoch + 1,
                "Vault: nonconsecutive handover");
            if (i != 0) require(certificates[i - 1].toEpoch == c.fromEpoch, "Vault: broken handover chain");
            require(c.frozenNextBatchSeq != 0, "Vault: invalid cutoff");
            AltBn128.G1Point memory next = AltBn128.canonical(AltBn128.G1Point(c.newPx, c.newPy));
            require(next.x == c.newPx && next.y == c.newPy, "Vault: noncanonical public key");
            bytes32 digest = handoverDigest(c);
            if (c.toEpoch <= authorityNonce) {
                                                                                            
                require(appliedCertificateDigest[c.toEpoch] == digest, "Vault: conflicting certificate");
                continue;
            }
            require(c.fromEpoch == authorityNonce, "Vault: missing prior handover");
            require(nextBatchSeq == c.frozenNextBatchSeq, "Vault: pending old batches");
            require(verifySignature(P.x, P.y, c.rx, c.ry, c.s, digest), "Vault: invalid pubupdate signature");
            require(verifySignature(G.x, G.y, c.guardianRx, c.guardianRy, c.guardianS, digest),
                "Vault: invalid guardian pubupdate signature");
            appliedCertificateDigest[c.toEpoch] = digest;
            _setAuthority(next, c.toEpoch);
        }
    }
}

contract SchnorrExampleV3 is SchnorrVaultV3 {
    constructor(uint256 px, uint256 py, uint256 gx, uint256 gy, uint256 epoch, uint256 dexChainId_)
        SchnorrVaultV3(px, py, gx, gy, epoch, dexChainId_) {}
}

contract SchnorrExampleTronV3 is SchnorrVaultV3 {
    address public immutable nonstandardToken;
    constructor(uint256 px, uint256 py, uint256 gx, uint256 gy, uint256 epoch, uint256 dexChainId_, address token)
        SchnorrVaultV3(px, py, gx, gy, epoch, dexChainId_)
    {
        require(token == address(0) || token.code.length != 0, "Vault: token has no code");
        nonstandardToken = token;
    }
    function _acceptFalseDepositReturn(address token) internal view override returns (bool) {
        return token == nonstandardToken;
    }
}
