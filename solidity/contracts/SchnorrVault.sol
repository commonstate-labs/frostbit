// SPDX-License-Identifier: MIT
pragma solidity >=0.8.18 <0.9.0;

library AltBn128 {
    uint256 internal constant P =
        21888242871839275222246405745257275088696311157297823662689037894645226208583;
    uint256 internal constant N =
        21888242871839275222246405745257275088548364400416034343698204186575808495617;

    struct G1Point { uint256 x; uint256 y; }

                                                                                             
    function valid(G1Point memory p) internal pure returns (bool) {
        if (p.x >= P || p.y >= P || (p.x == 0 && p.y == 0)) return false;
        return mulmod(p.y, p.y, P) == addmod(mulmod(mulmod(p.x, p.x, P), p.x, P), 3, P);
    }

    function canonical(G1Point memory p) internal pure returns (G1Point memory) {
        require(valid(p), "Vault: invalid public key");
        if (p.y > P / 2) p.y = P - p.y;
        return p;
    }

    function ecAdd(G1Point memory p, G1Point memory q) internal view returns (G1Point memory r) {
        uint256[4] memory input = [p.x, p.y, q.x, q.y];
        bool success;
        assembly { success := staticcall(gas(), 0x06, input, 0x80, r, 0x40) }
        require(success, "Vault: ecAdd failed");
    }

    function ecMul(G1Point memory p, uint256 s) internal view returns (G1Point memory r) {
        uint256[3] memory input = [p.x, p.y, s];
        bool success;
        assembly { success := staticcall(gas(), 0x07, input, 0x60, r, 0x40) }
        require(success, "Vault: ecMul failed");
    }

    function negate(G1Point memory p) internal pure returns (G1Point memory) {
        if (p.x == 0 && p.y == 0) return p;
        return G1Point(p.x, P - p.y);
    }
}

library SchnorrBN128 {
    function verify(AltBn128.G1Point memory pubkey, AltBn128.G1Point memory r, uint256 s, bytes32 digest)
        internal view returns (bool)
    {
        if (!AltBn128.valid(pubkey) || !AltBn128.valid(r) || s == 0 || s >= AltBn128.N) return false;
        pubkey = AltBn128.canonical(pubkey);
        uint256 e = uint256(keccak256(abi.encodePacked(r.x, r.y, pubkey.x, pubkey.y, digest))) % AltBn128.N;
        AltBn128.G1Point memory sG = AltBn128.ecMul(AltBn128.G1Point(1, 2), s);
        AltBn128.G1Point memory eP = AltBn128.ecMul(pubkey, e);
        AltBn128.G1Point memory expectedR = AltBn128.ecAdd(sG, AltBn128.negate(eP));
        return expectedR.x == r.x && expectedR.y == r.y;
    }
}

                                                                                         
abstract contract SchnorrVault {
    bytes32 public constant WITHDRAW_DOMAIN = keccak256("DEX_VAULT_MIXED_WITHDRAW_BATCH_V1");
    uint256 public constant MAX_WITHDRAWALS_PER_BATCH = 64;
    uint256 public constant TOKEN_CALL_GAS = 100_000;
    uint256 public constant NATIVE_CALL_GAS = 50_000;
    uint256 private constant TOKEN_ATTEMPT_GAS = 125_000;
    bytes4 private constant TRANSFER_SELECTOR = bytes4(keccak256("transfer(address,uint256)"));
    uint256 public immutable dexChainId;
    AltBn128.G1Point public P;
                                                                                  
    AltBn128.G1Point public G;
    uint256 public authorityNonce;
    uint256 public nextBatchSeq = 1;
                                                                 
    mapping(bytes32 => bool) public consumed;
    mapping(address => mapping(address => uint256)) public claimableReward;
    uint256 private entered;

    struct WithdrawalItem {
        bytes32 requestId;
        address token;
        address to;
        uint256 grossAmount;
        uint256 rewardIndex;
    }

    struct WithdrawalBatch {
        bytes32 domain;
        uint256 chainId;
        address vault;
        uint256 epoch;
        uint256 batchSeq;
        address[] rewardTokens;
        WithdrawalItem[] items;
    }

    struct Signature {
        uint256 rx;
        uint256 ry;
        uint256 s;
    }

    struct Handover {
        uint256 newPx;
        uint256 newPy;
        uint256 nextEpoch;
        uint256 rx;
        uint256 ry;
        uint256 s;
    }

    event PubChanged(uint256 oldPx, uint256 oldPy, uint256 newPx, uint256 newPy);
    event WithdrawalAttempted(
        bytes32 indexed requestId,
        address indexed to,
        address indexed token,
        uint256 grossAmount,
        uint256 fee,
        uint256 netAmount,
        bool attemptSucceeded
    );
    event WithdrawalBatchExecuted(
        bytes32 indexed digest,
        uint256 indexed epoch,
        uint256 indexed batchSeq,
        address broadcaster,
        uint256 itemCount,
        uint256 rewardTokenCount
    );
    event RewardClaimed(address indexed broadcaster, address indexed token, address indexed to, uint256 amount);
    event Deposited(
        uint256 indexed dexChainId,
        address indexed vault,
        address indexed token,
        string receiver,
        uint256 amount
    );

    constructor(uint256 px, uint256 py, uint256 gx, uint256 gy, uint256 initialEpoch, uint256 dexChainId_) {
        require(dexChainId_ != 0 && dexChainId_ <= type(uint64).max, "Vault: invalid DEX chain");
        dexChainId = dexChainId_;
        P = AltBn128.canonical(AltBn128.G1Point(px, py));
        G = AltBn128.canonical(AltBn128.G1Point(gx, gy));
        require(P.x != G.x || P.y != G.y, "Vault: operator equals guardian");
        authorityNonce = initialEpoch;
    }

    modifier nonReentrant() {
        require(entered == 0, "Vault: reentrant call");
        entered = 1;
        _;
        entered = 0;
    }

    function verifySignature(uint256 px, uint256 py, uint256 rx, uint256 ry, uint256 s, bytes32 digest)
        public view returns (bool)
    {
        return SchnorrBN128.verify(AltBn128.G1Point(px, py), AltBn128.G1Point(rx, ry), s, digest);
    }

                                                                                
    function depositNative(string calldata receiver) external payable nonReentrant {
        _validateDeposit(msg.value, receiver);
        emit Deposited(dexChainId, address(this), address(0), receiver, msg.value);
    }

    function depositToken(address token, uint256 amount, string calldata receiver) external nonReentrant {
        _validateDeposit(amount, receiver);
        require(token.code.length != 0, "Vault: token has no code");
        uint256 beforeBalance = _balanceOf(token, address(this));
        (bool ok, bytes memory result) = token.call(abi.encodeWithSelector(
            bytes4(keccak256("transferFrom(address,address,uint256)")), msg.sender, address(this), amount
        ));
        require(ok, "Vault: token call failed");
        require(result.length == 0 || result.length == 32, "Vault: invalid token return");
        if (result.length != 0) {
            bool transferred = abi.decode(result, (bool));
            require(transferred || _acceptFalseDepositReturn(token), "Vault: token transfer failed");
        }
        uint256 afterBalance = _balanceOf(token, address(this));
        require(afterBalance >= beforeBalance && afterBalance - beforeBalance == amount,
            "Vault: inexact token receipt");
        emit Deposited(dexChainId, address(this), token, receiver, amount);
    }

    function _validateDeposit(uint256 amount, string calldata receiver) private pure {
        require(amount != 0, "Vault: empty amount");
                                                                                           
        require(bytes(receiver).length != 0 && bytes(receiver).length <= 90,
            "Vault: invalid receiver length");
    }

    function _acceptFalseDepositReturn(address) internal view virtual returns (bool) { return false; }

    function _balanceOf(address token, address owner) internal view returns (uint256) {
        (bool ok, bytes memory result) = token.staticcall(
            abi.encodeWithSelector(bytes4(keccak256("balanceOf(address)")), owner)
        );
        require(ok && result.length == 32, "Vault: balance query failed");
        return abi.decode(result, (uint256));
    }

                                                                                 
                                                                                          
    function withdrawBatch(WithdrawalBatch calldata batch, Signature calldata signature, Signature calldata guardianSignature)
        external nonReentrant
    {
        (bytes32 digest, uint256[] memory rewards) = _validateBatch(batch);
        require(batch.epoch == authorityNonce, "Vault: wrong epoch");
        require(
            verifySignature(P.x, P.y, signature.rx, signature.ry, signature.s, digest),
            "Vault: invalid signature"
        );
        require(verifySignature(G.x, G.y, guardianSignature.rx, guardianSignature.ry, guardianSignature.s, digest),
            "Vault: invalid guardian signature");
        _consumeAccrueAndExecute(batch, digest, rewards);
    }

    function claimReward(address token, address to) external nonReentrant
    {
        require(to != address(0) && to != address(this), "Vault: invalid reward recipient");
        uint256 amount = claimableReward[token][msg.sender];
        require(amount != 0, "Vault: no reward");
        claimableReward[token][msg.sender] = 0;

        bool paid;
        if (token == address(0)) {
            (paid, ) = payable(to).call{value: amount, gas: NATIVE_CALL_GAS}("");
        } else if (token.code.length != 0) {
            paid = _safeTokenCall(token, to, amount);
        }
        require(paid, "Vault: reward transfer failed");
        emit RewardClaimed(msg.sender, token, to, amount);
    }

    function _validateBatch(WithdrawalBatch calldata batch)
        internal view returns (bytes32 digest, uint256[] memory rewards)
    {
        require(batch.domain == WITHDRAW_DOMAIN, "Vault: wrong domain");
        require(batch.chainId == block.chainid && batch.vault == address(this), "Vault: wrong target");
        require(nextBatchSeq != type(uint256).max, "Vault: batch sequence exhausted");
        require(batch.batchSeq == nextBatchSeq, "Vault: wrong batch sequence");
        uint256 length = batch.items.length;
        require(length != 0 && length <= MAX_WITHDRAWALS_PER_BATCH, "Vault: invalid batch size");
        uint256 tokenCount = batch.rewardTokens.length;
        require(tokenCount != 0 && tokenCount <= length, "Vault: invalid reward tokens");
        for (uint256 i = 1; i < tokenCount; ++i) {
            require(
                uint160(batch.rewardTokens[i - 1]) < uint160(batch.rewardTokens[i]),
                "Vault: reward tokens not sorted"
            );
        }
        rewards = new uint256[](tokenCount);
        for (uint256 i; i < length; ++i) {
            WithdrawalItem calldata item = batch.items[i];
            require(item.requestId != bytes32(0), "Vault: empty request id");
            require(item.rewardIndex < tokenCount, "Vault: reward index out of range");
            require(
                item.token == batch.rewardTokens[item.rewardIndex] &&
                item.to != address(0) && item.to != address(this),
                "Vault: invalid payment"
            );
            uint256 fee = _withdrawalFee(item.grossAmount);
            require(item.grossAmount > fee, "Vault: invalid payment");
            require(!consumed[item.requestId], "Vault: already consumed");
            rewards[item.rewardIndex] += fee;
        }
        for (uint256 i; i < tokenCount; ++i) {
            require(rewards[i] != 0, "Vault: unreferenced reward token");
        }
        digest = keccak256(abi.encode(batch));
    }

    function _withdrawalFee(uint256 grossAmount) private pure returns (uint256 fee) {
        fee = grossAmount / 10_000;
        if (grossAmount % 10_000 != 0) ++fee;
    }

    function _changePubDigest(uint256 nextNonce, uint256 newPx, uint256 newPy)
        private view returns (bytes32)
    {
        return keccak256(abi.encode(block.chainid, address(this), nextNonce, newPx, newPy));
    }

    function _setAuthority(AltBn128.G1Point memory next, uint256 nextNonce) internal {
        require(next.x != G.x || next.y != G.y, "Vault: operator equals guardian");
        AltBn128.G1Point memory old = P;
        P = next;
        authorityNonce = nextNonce;
        emit PubChanged(old.x, old.y, next.x, next.y);
    }

    function _consumeAccrueAndExecute(
        WithdrawalBatch calldata batch,
        bytes32 digest,
        uint256[] memory rewards
    ) internal {
        uint256 length = batch.items.length;

                                                                                 
                                                                                  
        for (uint256 i; i < length; ++i) {
            bytes32 requestId = batch.items[i].requestId;
            require(!consumed[requestId], "Vault: duplicate request");
            consumed[requestId] = true;
        }
        nextBatchSeq = batch.batchSeq + 1;
        uint256 tokenCount = batch.rewardTokens.length;
        for (uint256 i; i < tokenCount; ++i) {
            claimableReward[batch.rewardTokens[i]][msg.sender] += rewards[i];
        }

        for (uint256 i; i < length; ++i) {
            WithdrawalItem calldata item = batch.items[i];
            uint256 fee = _withdrawalFee(item.grossAmount);
            uint256 netAmount = item.grossAmount - fee;
            bool attemptSucceeded;
            if (item.token == address(0)) {
                (attemptSucceeded, ) = payable(item.to).call{value: netAmount, gas: NATIVE_CALL_GAS}("");
            } else {
                attemptSucceeded = _attemptTokenTransfer(item.token, item.to, netAmount);
            }
            emit WithdrawalAttempted(
                item.requestId,
                item.to,
                item.token,
                item.grossAmount,
                fee,
                netAmount,
                attemptSucceeded
            );
        }
        emit WithdrawalBatchExecuted(digest, batch.epoch, batch.batchSeq, msg.sender, length, tokenCount);
    }

    function _attemptTokenTransfer(address token, address to, uint256 amount) private returns (bool ok) {
        if (token.code.length == 0) return false;
        bytes memory data = abi.encodeCall(this.isolatedTokenTransfer, (token, to, amount));
        assembly {
            ok := call(TOKEN_ATTEMPT_GAS, address(), 0, add(data, 0x20), mload(data), 0, 0)
        }
    }

                                                                                 
                                                                                
                                                              
    function isolatedTokenTransfer(address token, address to, uint256 amount) external {
        require(msg.sender == address(this), "Vault: only self");
        require(_safeTokenCall(token, to, amount), "Vault: token transfer failed");
    }

    function _safeTokenCall(address token, address to, uint256 amount) private returns (bool accepted) {
        uint256 selector = uint32(TRANSFER_SELECTOR);
        uint256 returnSize;
        uint256 returnWord;
        bool callSucceeded;
        assembly {
            let ptr := mload(0x40)
            mstore(ptr, shl(224, selector))
            mstore(add(ptr, 0x04), to)
            mstore(add(ptr, 0x24), amount)
            callSucceeded := call(TOKEN_CALL_GAS, token, 0, ptr, 0x44, 0, 0)
            returnSize := returndatasize()
            if and(callSucceeded, eq(returnSize, 0x20)) {
                returndatacopy(ptr, 0, 0x20)
                returnWord := mload(ptr)
            }
        }
        accepted = callSucceeded && (returnSize == 0 || (returnSize == 32 && returnWord == 1));
    }

                                                                                      
    receive() external payable {}
    fallback() external payable { revert("Vault: unknown selector"); }
}
