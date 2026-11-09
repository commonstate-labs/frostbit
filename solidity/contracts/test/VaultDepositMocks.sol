// SPDX-License-Identifier: MIT
pragma solidity >=0.8.18 <0.9.0;

contract DepositTokenMock {
    enum Mode { Standard, NoReturn, FalseReturn, FalseNoTransfer, Fee, Malformed, RevertCall, Excess }
    mapping(address => uint256) public balanceOf;
    mapping(address => mapping(address => uint256)) public allowance;
    Mode public mode;
    address public callbackTarget;
    bytes public callbackData;
    bool public callbackSucceeded;
    bytes public callbackResult;

    function mint(address to, uint256 amount) external { balanceOf[to] += amount; }
    function setMode(Mode value) external { mode = value; }
    function approve(address spender, uint256 amount) external returns (bool) {
        allowance[msg.sender][spender] = amount;
        return true;
    }
    function configureCallback(address target, bytes calldata data) external {
        callbackTarget = target;
        callbackData = data;
    }
    function transferFrom(address from, address to, uint256 amount) external returns (bool) {
        allowance[from][msg.sender] -= amount;
        return _transfer(from, to, amount);
    }
    function transfer(address to, uint256 amount) external returns (bool) {
        return _transfer(msg.sender, to, amount);
    }
    function _transfer(address from, address to, uint256 amount) private returns (bool) {
        require(mode != Mode.RevertCall, "mock transfer reverted");
        if (mode == Mode.FalseNoTransfer) return false;
        balanceOf[from] -= amount;
        balanceOf[to] += mode == Mode.Fee ? amount - 1 : mode == Mode.Excess ? amount + 1 : amount;
        if (callbackTarget != address(0)) {
            (callbackSucceeded, callbackResult) = callbackTarget.call(callbackData);
        }
        if (mode == Mode.NoReturn) assembly { return(0, 0) }
        if (mode == Mode.Malformed) assembly { mstore(0, 1) return(31, 1) }
        return mode != Mode.FalseReturn;
    }
}

contract BrokenDepositBalanceToken {
    bool public immutable badAfter;
    bool public transferAttempted;
    constructor(bool badAfter_) { badAfter = badAfter_; }
    function balanceOf(address) external view returns (uint256) {
        if (transferAttempted == badAfter) assembly { mstore(0, 0) return(0, 1) }
        return 0;
    }
    function transferFrom(address, address, uint256) external returns (bool) {
        transferAttempted = true;
        return true;
    }
}

contract DepositNativeReceiver {
    bool public accepting = true;
    uint256 public received;
    address public callbackTarget;
    bytes public callbackData;
    bool public callbackSucceeded;
    bytes public callbackResult;
    function setAccepting(bool value) external { accepting = value; }
    function configureCallback(address target, bytes calldata data) external {
        callbackTarget = target;
        callbackData = data;
    }
    receive() external payable {
        require(accepting, "mock native rejected");
        received += msg.value;
        if (callbackTarget != address(0)) {
            (callbackSucceeded, callbackResult) = callbackTarget.call{value: msg.value}(callbackData);
        }
    }
}
