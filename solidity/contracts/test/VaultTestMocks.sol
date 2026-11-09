// SPDX-License-Identifier: MIT
pragma solidity >=0.8.18 <0.9.0;

import "../SchnorrVault.sol";

contract SchnorrVaultSequenceHarness is SchnorrVault {
    constructor(uint256 px, uint256 py, uint256 gx, uint256 gy, uint256 initialEpoch, uint256 dexChainId_)
        SchnorrVault(px, py, gx, gy, initialEpoch, dexChainId_) {}

    function forceNextBatchSeq(uint256 value) external { nextBatchSeq = value; }
}

contract StandardToken {
    mapping(address => uint256) public balanceOf;
    function mint(address to, uint256 amount) external { balanceOf[to] += amount; }
    function transfer(address to, uint256 amount) external returns (bool) {
        balanceOf[msg.sender] -= amount;
        balanceOf[to] += amount;
        return true;
    }
}

contract NoReturnToken {
    mapping(address => uint256) public balanceOf;
    function mint(address to, uint256 amount) external { balanceOf[to] += amount; }
    function transfer(address to, uint256 amount) external {
        balanceOf[msg.sender] -= amount;
        balanceOf[to] += amount;
    }
}

contract FalseReturnToken {
    mapping(address => uint256) public balanceOf;
    uint256 public immutable shortfall;
    constructor(uint256 missing) { shortfall = missing; }
    function mint(address to, uint256 amount) external { balanceOf[to] += amount; }
    function transfer(address to, uint256 amount) external returns (bool) {
        balanceOf[msg.sender] -= amount;
        balanceOf[to] += amount > shortfall ? amount - shortfall : 0;
        return false;
    }
}

contract MalformedReturnToken {
    mapping(address => uint256) public balanceOf;
    function mint(address to, uint256 amount) external { balanceOf[to] += amount; }
    function transfer(address to, uint256 amount) external returns (bool) {
        balanceOf[msg.sender] -= amount;
        balanceOf[to] += amount;
        assembly { mstore(0, 1) return(31, 1) }
    }
}

contract RevertingToken {
    function transfer(address, uint256) external pure returns (bool) { revert("mock revert"); }
}

contract HugeReturnToken {
    function transfer(address, uint256) external pure returns (bool) {
        assembly {
            mstore(0, 1)
            return(0, 0x10000)
        }
    }
}

contract GasBurnerToken {
    function transfer(address, uint256) external pure {
        assembly { for { } 1 { } { } }
    }
}

contract ReentrantToken {
    mapping(address => uint256) public balanceOf;
    address public callbackTarget;
    bytes public callbackData;
    bool public callbackSucceeded;

    function mint(address to, uint256 amount) external { balanceOf[to] += amount; }
    function configure(address target, bytes calldata data) external {
        callbackTarget = target;
        callbackData = data;
    }
    function transfer(address to, uint256 amount) external returns (bool) {
        balanceOf[msg.sender] -= amount;
        balanceOf[to] += amount;
        (callbackSucceeded, ) = callbackTarget.call(callbackData);
        return true;
    }
}

contract SelectiveToken {
    mapping(address => uint256) public balanceOf;
    mapping(address => bool) public blocked;
    function mint(address to, uint256 amount) external { balanceOf[to] += amount; }
    function setBlocked(address to, bool value) external { blocked[to] = value; }
    function transfer(address to, uint256 amount) external returns (bool) {
        balanceOf[msg.sender] -= amount;
        balanceOf[to] += amount;
        return !blocked[to];
    }
}

contract StorageReceiver {
    uint256 public received;
    bool public accepting = true;
    function setAccepting(bool enabled) external { accepting = enabled; }
    receive() external payable { require(accepting, "rejected"); received += msg.value; }
}

contract ReentrantReceiver {
    address public target;
    bytes public callback;
    bool public callbackSucceeded;
    uint256 public received;
    function configure(address target_, bytes calldata callback_) external {
        target = target_;
        callback = callback_;
    }
    receive() external payable {
        received += msg.value;
        (callbackSucceeded, ) = target.call(callback);
    }
}
