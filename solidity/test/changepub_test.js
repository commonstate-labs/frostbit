const { expect } = require('chai');
const { ethers } = require('hardhat');
const {SECRET_A,SECRET_B,GUARDIAN_SECRET,point,canonical,batch,batchSignature,guardianPoint,guardianSignature,dualHandover}=require('./helpers/schnorr');
describe('Dual custody authorization',function(){
 let vault,recipient;
 beforeEach(async function(){[,recipient]=await ethers.getSigners();vault=await(await ethers.getContractFactory('SchnorrExampleV3')).deploy(...await point(SECRET_A),...await guardianPoint(),1n,51025n);await vault.waitForDeployment();await(await ethers.getSigners())[0].sendTransaction({to:await vault.getAddress(),value:10000n});});
 it('rejects either party alone without consuming a request',async function(){
  const value=await batch(vault,recipient.address),op=await batchSignature(value),guard=await guardianSignature(value),zero=[0n,0n,0n];
  await expect(vault.withdrawBatch(value,op,zero)).to.be.revertedWith('Vault: invalid guardian signature');
  await expect(vault.withdrawBatch(value,zero,guard)).to.be.revertedWith('Vault: invalid signature');
  await expect(vault.withdrawBatch(value,op,await batchSignature(value,SECRET_B))).to.be.revertedWith('Vault: invalid guardian signature');
  expect(await vault.nextBatchSeq()).to.equal(1n);expect(await vault.consumed(value.items[0].requestId)).to.equal(false);
  await vault.withdrawBatch(value,op,guard);expect(await vault.nextBatchSeq()).to.equal(2n);
 });
 it('requires both authorizations to install a new operator and keeps guardian fixed',async function(){
  const next=await point(SECRET_B),c=await dualHandover(vault,next,2n);
  await expect(vault.catchUpAuthority([{...c,guardianS:0n}])).to.be.revertedWith('Vault: invalid guardian pubupdate signature');
  await expect(vault.catchUpAuthority([{...c,s:0n}])).to.be.revertedWith('Vault: invalid pubupdate signature');
  expect(await vault.authorityNonce()).to.equal(1n);
  await vault.catchUpAuthority([c]);expect(await vault.authorityNonce()).to.equal(2n);expect(Array.from(await vault.G())).to.deep.equal(canonical(await guardianPoint()));
  const value=await batch(vault,recipient.address);
  await vault.withdrawBatch(value,await batchSignature(value,SECRET_B),await guardianSignature(value));
 });
 it('has no single-authority ABI and rejects malformed guardian configuration',async function(){
  expect(vault.interface.getFunction('withdrawBatch((bytes32,uint256,address,uint256,uint256,address[],(bytes32,address,address,uint256,uint256)[]),(uint256,uint256,uint256))')).to.equal(null);
  expect(vault.interface.getFunction('withdrawBatchWithHandover')).to.equal(null);
  const Factory=await ethers.getContractFactory('SchnorrExampleV3');await expect(Factory.deploy(...await point(SECRET_A),0n,0n,1n,51025n)).to.be.revertedWith('Vault: invalid public key');
 });
});
