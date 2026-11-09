// SPDX-License-Identifier: MIT
pragma solidity 0.8.18;

contract ExecutionProbe {
    function chainId() external view returns(uint256) { return block.chainid; }
    function ecMul(uint256 scalar) external view returns(uint256 x,uint256 y) {
        uint256[3] memory input=[uint256(1),uint256(2),scalar];
        uint256[2] memory output;
        bool ok;
        assembly { ok := staticcall(gas(),7,input,96,output,64) }
        require(ok,"ecmul");
        return(output[0],output[1]);
    }
}
contract StandardToken {
    mapping(address=>uint256) public balanceOf;
    function mint(address to,uint256 amount) external { balanceOf[to]+=amount; }
    function transfer(address to,uint256 amount) external returns(bool) {
        balanceOf[msg.sender]-=amount;balanceOf[to]+=amount;return true;
    }
}
contract NoReturnToken {
    mapping(address=>uint256) public balanceOf;
    function mint(address to,uint256 amount) external { balanceOf[to]+=amount; }
    function transfer(address to,uint256 amount) external {
        balanceOf[msg.sender]-=amount;balanceOf[to]+=amount;
    }
}
contract FalseReturnToken {
    mapping(address=>uint256) public balanceOf;
    function mint(address to,uint256 amount) external { balanceOf[to]+=amount; }
    function transfer(address to,uint256 amount) external returns(bool) {
        balanceOf[msg.sender]-=amount;balanceOf[to]+=amount;return false;
    }
}
contract LyingFalseToken {
    mapping(address=>uint256) public balanceOf;
    function mint(address to,uint256 amount) external { balanceOf[to]+=amount; }
    function transfer(address,uint256) external pure returns(bool) { return false; }
}
contract StorageReceiver {
    uint256 public received;
    receive() external payable { received+=msg.value; }
}
