// SPDX-License-Identifier: MIT
pragma solidity 0.8.18;

import "./SchnorrVault.sol";

contract SchnorrExampleTron is SchnorrVault {
                                                                                  
    address public immutable nonstandardToken;

    constructor(uint256 px, uint256 py, uint256 gx, uint256 gy, uint256 initialEpoch, uint256 dexChainId_, address falseReturningToken)
        SchnorrVault(px, py, gx, gy, initialEpoch, dexChainId_)
    {
        require(falseReturningToken == address(0) || falseReturningToken.code.length != 0, "Vault: token has no code");
        nonstandardToken = falseReturningToken;
    }

    function _acceptFalseDepositReturn(address token) internal view override returns (bool) {
        return token == nonstandardToken;
    }

}
