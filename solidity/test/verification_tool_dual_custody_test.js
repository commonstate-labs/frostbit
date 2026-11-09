const {expect}=require('chai');
const {ethers}=require('hardhat');
const {A,B,G,point,canonicalSecret,sign,withdrawalBatch,batchHash,changeHash,handoverCertificate}=require('../verification/common.cjs');
describe('Offline verification tools use production V3 authorization',function(){
 it('uses both JS signatures for batch and a canonical eleven-word handover',async function(){
  const [funder,recipient]=await ethers.getSigners();const v=await(await ethers.getContractFactory('SchnorrExampleV3')).deploy(...point(A),...point(canonicalSecret(G)),1,51025);await v.waitForDeployment();const address=await v.getAddress();const chainId=(await ethers.provider.getNetwork()).chainId;await funder.sendTransaction({to:address,value:10000});
  const batch=withdrawalBatch(chainId,address,1,1,'tool-first',1001,ethers.ZeroAddress,recipient.address);
  await expect(v.withdrawBatch(batch,sign(batchHash(batch),A),sign(batchHash(batch),B))).to.be.revertedWith('Vault: invalid guardian signature');
  await v.withdrawBatch(batch,sign(batchHash(batch),A),sign(batchHash(batch),G));
  const cert=handoverCertificate(chainId,address,2n,A,B,2n);expect(cert.length).to.equal(11);expect(await v.handoverDigest(cert)).to.equal(changeHash(chainId,address,2n,point(canonicalSecret(B)),2n));
  await v.catchUpAuthority([cert]);expect(await v.authorityNonce()).to.equal(2n);
  const next=withdrawalBatch(chainId,address,2,2,'tool-second',1001,ethers.ZeroAddress,recipient.address);await v.withdrawBatch(next,sign(batchHash(next),B),sign(batchHash(next),G));expect(await v.nextBatchSeq()).to.equal(3n);
 });
});
