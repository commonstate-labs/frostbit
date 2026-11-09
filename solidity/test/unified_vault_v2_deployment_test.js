const { expect } = require('chai');
const hre = require('hardhat');
const fs = require('fs'), os = require('os'), path = require('path');
const { Wallet, ethers } = require('ethers');
const tools = require('../scripts/vault_deployment');
const {guardianEnvironment}=require('./helpers/guardian-deployment');
const deployment = require('../scripts/deploy_schnorr_vault');
describe('V3 explicit deployment selection and recovery', function () {
  let artifact, directory;
  before(async () => { artifact = await tools.loadVaultArtifact(hre, 'SchnorrExampleV3'); });
  beforeEach(() => { directory = fs.mkdtempSync(path.join(fs.realpathSync(os.tmpdir()), 'dex-v2-deploy-')); });
  afterEach(() => { expect(path.dirname(directory)).to.equal(fs.realpathSync(os.tmpdir())); expect(path.basename(directory)).to.match(/^dex-v2-deploy-/); fs.rmSync(directory, {recursive:true,force:true}); });
  function options(protocol) { return deployment.parseOptions({ ...guardianEnvironment(directory),VAULT_CHAIN_ID:'31337',VAULT_DEX_CHAIN_ID:'51025',VAULT_ID:'0',VAULT_EPOCH:'1',VAULT_PX:'1',VAULT_PY:'2',VAULT_AUTHORITY_PROTOCOL:protocol,VAULT_OUTPUT:path.join(directory,'vault.json') },'hardhat'); }
  async function local(protocolOverride, expectedProtocol = 3) {
    const signer=Wallet.createRandom().connect(hre.ethers.provider);await hre.ethers.provider.send('hardhat_setBalance',[signer.address,'0x1000000000000000000']);let broadcasts=0;
    const provider={
      getNetwork:()=>hre.ethers.provider.getNetwork(),getBlockNumber:()=>hre.ethers.provider.getBlockNumber(),
      send:async(method,params)=>{if(method!=='eth_getBlockReceipts')return hre.ethers.provider.send(method,params);const block=await hre.ethers.provider.send('eth_getBlockByNumber',[params[0],false]);return Promise.all(block.transactions.map(hash=>hre.ethers.provider.send('eth_getTransactionReceipt',[hash])));},
      broadcastTransaction:async raw=>{expect(JSON.parse(fs.readFileSync(options().output)).authorityProtocol ?? 1).to.equal(expectedProtocol);broadcasts++;return hre.ethers.provider.broadcastTransaction(raw);},
      getTransactionReceipt:hash=>hre.ethers.provider.getTransactionReceipt(hash),getTransaction:hash=>hre.ethers.provider.getTransaction(hash),getCode:address=>hre.ethers.provider.getCode(address),getBlock:number=>hre.ethers.provider.getBlock(number),
      call:request=>{if(protocolOverride!==undefined&&request.data===new ethers.Interface(artifact.abi).encodeFunctionData('AUTHORITY_PROTOCOL'))return ethers.AbiCoder.defaultAbiCoder().encode(['uint256'],[protocolOverride]);return hre.ethers.provider.call(request);},
    };return{provider,signer,broadcasts:()=>broadcasts};
  }
  it('defaults to V3 and rejects unknown versions or mismatched artifact',function(){expect(options().authorityProtocol).to.equal(3);for(const version of ['0','1','2','03','',null])expect(()=>options(version)).to.throw('AUTHORITY_PROTOCOL');expect(tools.validateVaultABI(artifact.abi,3)).to.equal(true);expect(()=>tools.validateVaultABI(artifact.abi.filter(row=>row.name!=='catchUpAuthority'),3)).to.throw('missing catchUpAuthority');});
  it('rejects an unexpected source or epoch zero before deployment', async function(){
    const changed = { ...await hre.artifacts.readArtifact('SchnorrExampleV3'), sourceName:'contracts/Other.sol' };
    await expect(tools.loadVaultArtifact({ artifacts:{readArtifact:async()=>changed} },'SchnorrExampleV3')).to.be.rejectedWith('unexpected source');
    expect(()=>tools.vaultIdentity({...options('3'),epoch:0n},artifact)).to.throw('V3 VAULT_EPOCH');
  });
  it('deploys exact V3 runtime and recovers without secrets or another payment',async function(){const deps=await local(),config=options('3');const result=await deployment.run(config,{artifact,...deps});expect(result.authorityProtocol).to.equal(3);expect(result.contract).to.equal('SchnorrExampleV3');expect(await hre.ethers.provider.getCode(result.address)).to.equal(tools.expectedVaultRuntime(artifact,51025n));const noKey={...config};Object.defineProperty(noKey,'privateKey',{enumerable:true,get(){throw Error('read private key')}});const recovered=await deployment.run(noKey,{artifact,provider:deps.provider});expect(recovered).to.deep.equal(result);expect(deps.broadcasts()).to.equal(1);});
  it('recovers a V3 journal using explicitly frozen artifacts without loading current defaults', async function(){
    const v1 = await tools.loadVaultArtifact(hre,'SchnorrExampleV3'), deps = await local(undefined,3), config=options('3');
    const result = await deployment.run(config,{artifact:v1,...deps});
    const frozen = path.join(directory,'old frozen artifacts');
    require('../../dev/server5/prepare-unified-vaults').captureArtifacts(path.join(__dirname,'../artifacts'),frozen,['SchnorrExampleV3']);
    const publicOnly={...config,artifactsDir:frozen};
    Object.defineProperty(publicOnly,'privateKey',{enumerable:true,get(){throw Error('frozen recovery read key')}});
    const currentUnavailable={artifacts:{readArtifact(){throw Error('read current artifact instead of frozen')}}};
    const recovered=await deployment.run(publicOnly,{provider:deps.provider,hre:currentUnavailable});
    expect(recovered).to.deep.equal(result);expect(deps.broadcasts()).to.equal(1);
    const filename=path.join(frozen,'contracts/SchnorrVaultV3.sol/SchnorrExampleV3.json');
    const artifact=JSON.parse(fs.readFileSync(filename));artifact.bytecode+='00';fs.writeFileSync(filename,JSON.stringify(artifact));
    await expect(deployment.run(publicOnly,{provider:deps.provider,hre:currentUnavailable})).to.be.rejectedWith('compiler build info');
    expect(JSON.parse(fs.readFileSync(config.output))).to.deep.equal(result);expect(deps.broadcasts()).to.equal(1);
  });
  it('keeps pending if claimed V3 capability mismatches despite exact runtime',async function(){const deps=await local(1);await expect(deployment.run(options('3'),{artifact,...deps})).to.be.rejectedWith('protocol/capabilities');expect(JSON.parse(fs.readFileSync(options().output)).status).to.equal('pending');expect(deps.broadcasts()).to.equal(1);});
  it('binds both custody keys and requires protocol in the journal',function(){const identity=tools.vaultIdentity(options('3'),artifact),output=options().output;tools.writeVaultPending(output,identity,{txHash:'0x'+'ab'.repeat(32),address:'0x'+'11'.repeat(20),deployer:'0x'+'22'.repeat(20)});const saved=JSON.parse(fs.readFileSync(output));expect(saved.authorityProtocol).to.equal(3);expect(saved.guardianPx).to.equal(identity.guardianPx);expect(tools.loadVaultJournal(output,identity).status).to.equal('pending');saved.guardianPx='1';fs.writeFileSync(output,JSON.stringify(saved));expect(()=>tools.loadVaultJournal(output,identity)).to.throw('guardianPx');expect(()=>tools.validateVaultCapabilities(3,3,9)).to.throw('capabilities');});
});
