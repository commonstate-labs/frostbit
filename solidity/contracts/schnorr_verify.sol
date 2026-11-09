// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "./SchnorrVault.sol";

contract SchnorrExample is SchnorrVault {
    constructor(uint256 px, uint256 py, uint256 gx, uint256 gy, uint256 initialEpoch, uint256 dexChainId_)
        SchnorrVault(px, py, gx, gy, initialEpoch, dexChainId_) {}
}
