   
               
                                                                                     
                                                                                                            
   

import { sign, toHex } from './crypto.js';
import { sha256 } from '@noble/hashes/sha256';
import { util as protobufUtil } from 'protobufjs/minimal';

                                                                              
import { pb } from './data_proto.js';

const AnyTx = () => pb.AnyTx;
const Transaction = () => pb.Transaction;
const BaseMessage = () => pb.BaseMessage;
const PublicKeys = () => pb.PublicKeys;
const FROST_WITHDRAW_ORIGINAL_REQUEST_MAX_BYTES = 16 * 1024;

function decodeOptionalBytes(value, fieldName) {
    if (value == null || value === '') return new Uint8Array();
    if (value instanceof Uint8Array) return value;
    if (Array.isArray(value)) return Uint8Array.from(value);
    if (typeof value !== 'string') throw new Error(`${fieldName} must be bytes, hex, or base64`);
    const raw = value.trim();
    if (!raw) return new Uint8Array();
    const hex = raw.replace(/^0x/i, '');
    if (/^[0-9a-f]+$/i.test(hex) && hex.length % 2 === 0) {
        const out = new Uint8Array(hex.length / 2);
        for (let i = 0; i < out.length; i++) out[i] = Number.parseInt(hex.slice(i * 2, i * 2 + 2), 16);
        return out;
    }
    try {
        const binary = atob(raw);
        return Uint8Array.from(binary, ch => ch.charCodeAt(0));
    } catch {
        throw new Error(`${fieldName} is neither canonical hex nor base64`);
    }
}

function isLong(value) {
    return protobufUtil.Long?.isLong(value) === true;
}

function isDefaultProtoValue(value, fieldDefault) {
                                                                                                    
                                                                                                
    if (fieldDefault == null) return false;
    if (fieldDefault === 0 || isLong(fieldDefault)) {
        return value === 0 || value === 0n || (typeof value === 'string' && /^0+$/.test(value)) || (isLong(value) && value.isZero());
    }
    if (typeof fieldDefault === 'string' || typeof fieldDefault === 'boolean') return value === fieldDefault;
    if (fieldDefault instanceof Uint8Array) return value instanceof Uint8Array && value.length === 0;
    if (Array.isArray(fieldDefault)) return Array.isArray(value) && value.length === 0;
    return false;
}

function normalizeForSigning(value) {
                                                                                              
    if (value == null || typeof value !== 'object' || value instanceof Uint8Array || isLong(value)) return value;
    if (Array.isArray(value)) {
        for (const item of value) normalizeForSigning(item);
        return value;
    }
    const prototype = Object.getPrototypeOf(value);
    for (const key of Object.keys(value)) {
        const field = value[key];
        const fieldDefault = prototype && Object.getOwnPropertyDescriptor(prototype, key)?.value;
        if (isDefaultProtoValue(field, fieldDefault)) {
            delete value[key];
        } else if (fieldDefault == null || Array.isArray(fieldDefault)) {
                                                                                               
            normalizeForSigning(field);
        }
    }
    return value;
}

   
                                                                                           
  
                                                                          
                                                                     
   
                                                                                                                 
                                                                                                       
export const DEFAULT_CHAIN_ID = 1;

export function buildAndSignTransferTx({ fromAddress, toAddress, tokenAddress, amount, nonce, rawPrivKey, pubKeyCompressed, fee, chainId }) {
    const pubKeysMsg = PublicKeys().create({ keys: { 1: pubKeyCompressed } });
    const baseDraft = BaseMessage().create({ fromAddress, nonce, publicKeys: pubKeysMsg, fee: fee || '0', chainId: chainId || DEFAULT_CHAIN_ID });
    const txMsg = Transaction().create({ base: baseDraft, to: toAddress, tokenAddress, amount });
    const anyTxDraft = AnyTx().create({ transaction: txMsg });

                                                
    normalizeForSigning(anyTxDraft);
    const draftBytes = AnyTx().encode(anyTxDraft).finish();
    const txId = '0x' + toHex(sha256(draftBytes));

                                                   
    const txIdBytes = new TextEncoder().encode(txId);
    const sigBytes = sign(rawPrivKey, txIdBytes);

    baseDraft.txId = txId;
    baseDraft.signature = sigBytes;
    return AnyTx().encode(normalizeForSigning(AnyTx().create({ transaction: txMsg }))).finish();
}

   
                                                              
                                                                    
   
export function buildAndSign(txDesc, { fromAddress, nonce, rawPrivKey, pubKeyCompressed, fee, chainId }) {
    const cid = chainId || DEFAULT_CHAIN_ID;
    const base = () => {
        const pubKeysMsg = PublicKeys().create({ keys: { 1: pubKeyCompressed } });
        return BaseMessage().create({ fromAddress, nonce, publicKeys: pubKeysMsg, fee: fee || '0', chainId: cid });
    };
    const signAndWrap = (anyTxContent) => {
        normalizeForSigning(anyTxContent);
        const draft = AnyTx().encode(AnyTx().create(anyTxContent)).finish();
        const txId = '0x' + toHex(sha256(draft));
        const txIdBytes = new TextEncoder().encode(txId);
        const sig = sign(rawPrivKey, txIdBytes);
        Object.values(anyTxContent)[0].base.txId = txId;
        Object.values(anyTxContent)[0].base.signature = sig;
        return AnyTx().encode(AnyTx().create(normalizeForSigning(anyTxContent))).finish();
    };

    switch (txDesc.type) {
        case 'transaction':
            return buildAndSignTransferTx({
                fromAddress, toAddress: txDesc.to, tokenAddress: txDesc.tokenAddress,
                amount: txDesc.amount, nonce, rawPrivKey, pubKeyCompressed, fee, chainId: cid,
            });

        case 'order': {
            const OrderTx = () => pb.OrderTx;
            const OrderOp = pb.OrderOp;
            const OrderSide = pb.OrderSide;
            const isRemove = txDesc.op === 'REMOVE';
            const tx = OrderTx().create({
                base: base(),
                baseToken: txDesc.baseToken || '',
                quoteToken: txDesc.quoteToken || '',
                op: isRemove ? OrderOp.REMOVE : OrderOp.ADD,
                opTargetId: txDesc.opTargetId || '',
                amount: txDesc.amount || '0',
                price: txDesc.price || '0',
                side: txDesc.side === 'SELL' ? OrderSide.SELL : OrderSide.BUY,
            });
            return signAndWrap({ orderTx: tx });
        }

        case 'witness_vote': {
            const WitnessVoteTx = () => pb.WitnessVoteTx;
            const WitnessVote = () => pb.WitnessVote;
            const WitnessVoteType = pb.WitnessVoteType;
            const voteType = txDesc.vote === 'FAIL' ? WitnessVoteType.VOTE_FAIL : WitnessVoteType.VOTE_PASS;
            const tx = WitnessVoteTx().create({
                base: base(),
                vote: WitnessVote().create({ requestId: txDesc.requestId, voteType, witnessAddress: fromAddress, round: txDesc.round || 0 }),
            });
            return signAndWrap({ witnessVoteTx: tx });
        }

        case 'witness_challenge': {
            if (!txDesc.stakeAmount) throw new Error('witness_challenge requires stakeAmount');
            const WitnessChallengeTx = () => pb.WitnessChallengeTx;
            const tx = WitnessChallengeTx().create({
                base: base(),
                requestId: txDesc.requestId,
                stakeAmount: txDesc.stakeAmount,
                reason: txDesc.reason || '',
                evidence: txDesc.evidence || '',
            });
            return signAndWrap({ witnessChallengeTx: tx });
        }

        case 'arbitration_vote': {
            if (typeof txDesc.challengeId !== 'string' || !txDesc.challengeId || typeof txDesc.requestId !== 'string' || !txDesc.requestId) {
                throw new Error('arbitration_vote requires challengeId and requestId');
            }
            const roundValue = txDesc.ballotRound;
            if (typeof roundValue === 'number' && !Number.isSafeInteger(roundValue)) {
                throw new Error('arbitration_vote ballotRound must be an exact uint64');
            }
            const ballotRound = String(roundValue ?? '');
            if (!/^[1-9]\d*$/.test(ballotRound) || BigInt(ballotRound) > 18446744073709551615n) {
                throw new Error('arbitration_vote requires a positive uint64 ballotRound');
            }
            if (!['PASS', 'FAIL', 'ABSTAIN'].includes(txDesc.vote)) {
                throw new Error('arbitration_vote requires PASS, FAIL, or ABSTAIN');
            }
            const ArbitrationVoteTx = () => pb.ArbitrationVoteTx;
            const WitnessVote = () => pb.WitnessVote;
            const WitnessVoteType = pb.WitnessVoteType;
            const voteType = WitnessVoteType['VOTE_' + txDesc.vote];
            const tx = ArbitrationVoteTx().create({
                base: base(),
                challengeId: txDesc.challengeId,
                ballotRound,
                vote: WitnessVote().create({
                    requestId: txDesc.requestId,
                    witnessAddress: fromAddress,
                    ballotRound,
                    voteType,
                    reason: txDesc.reason || '',
                }),
            });
            return signAndWrap({ arbitrationVoteTx: tx });
        }

        case 'recharge_request': {
            const WitnessRequestTx = () => pb.WitnessRequestTx;
                                                                                            
            const scriptHex = (txDesc.nativeScript || '').replace(/^0x/i, '');
            const nativeScript = scriptHex.length > 0
                ? new Uint8Array(scriptHex.match(/.{1,2}/g).map(b => parseInt(b, 16)))
                : new Uint8Array(0);
            const tx = WitnessRequestTx().create({
                base: base(),
                nativeChain: txDesc.chain,
                nativeTxHash: txDesc.nativeTxHash,
                nativeVout: txDesc.nativeVout ?? 0,
                nativeScript,
				amount: String(txDesc.amount || ''),
                receiverAddress: txDesc.receiverAddress,
                memo: txDesc.memo || '',
                rechargeFee: String(txDesc.rechargeFee || '0'),
                nativeContract: txDesc.nativeContract || '',
                vaultId: txDesc.vaultId ?? 0,
                depositEventIndex: txDesc.depositEventIndex ?? 0,
                depositProtocol: txDesc.depositProtocol ?? 0,
            });
            return signAndWrap({ witnessRequestTx: tx });
        }

        case 'frost_withdraw': {
            const FrostWithdrawRequestTx = () => pb.FrostWithdrawRequestTx;
            const btc = btcWithdrawalAuthorization(txDesc);
                                                                                                   
            const tx = FrostWithdrawRequestTx().create({
                base: base(),
                asset: txDesc.asset,
                to: txDesc.toAddress,
                amount: txDesc.amount,
                ...btc,
            });
            return signAndWrap({ frostWithdrawRequestTx: tx });
        }

        case 'frost_withdraw_cancel': {
            const withdrawId = String(txDesc.withdrawId || '').trim();
            if (!withdrawId) throw new Error('frost_withdraw_cancel requires withdrawId');
            const originalRequestTx = decodeOptionalBytes(txDesc.originalRequestTx, 'originalRequestTx');
            if (originalRequestTx.length > FROST_WITHDRAW_ORIGINAL_REQUEST_MAX_BYTES) {
                throw new Error('originalRequestTx exceeds 16384 bytes');
            }
            const FrostWithdrawCancelTx = () => pb.FrostWithdrawCancelTx;
            const tx = FrostWithdrawCancelTx().create({
                base: base(),
                withdrawId,
                                                                                                                                     
                originalRequestTx,
            });
            return signAndWrap({ frostWithdrawCancelTx: tx });
        }

        case 'future_order': {
            const FutureOrderTx = () => pb.FutureOrderTx;
            const tx = FutureOrderTx().create({
                base: base(),
                pair: txDesc.pair,
                side: txDesc.side === 'SELL' ? 2 : 1,
                quantity: txDesc.amount,
                price: txDesc.price,
                leverage: parseInt(txDesc.leverage) || 1,
                isMarket: !!txDesc.isMarket,
                isClose: !!txDesc.isClose,
                closePositionId: txDesc.closePositionId || '',
            });
            return signAndWrap({ futureOrderTx: tx });
        }

        case 'future_add_margin': {
            const FutureAddMarginTx = () => pb.FutureAddMarginTx;
            const tx = FutureAddMarginTx().create({
                base: base(),
                positionId: txDesc.positionId,
                amount: String(txDesc.amount || ''),
            });
            return signAndWrap({ futureAddMarginTx: tx });
        }

        case 'future_remove_margin': {
            const FutureRemoveMarginTx = () => pb.FutureRemoveMarginTx;
            const tx = FutureRemoveMarginTx().create({
                base: base(),
                positionId: txDesc.positionId,
                amount: String(txDesc.amount || ''),
            });
            return signAndWrap({ futureRemoveMarginTx: tx });
        }

        case 'future_order_cancel': {
            const FutureOrderCancelTx = () => pb.FutureOrderCancelTx;
            const tx = FutureOrderCancelTx().create({
                base: base(),
                orderId: txDesc.orderId,
            });
            return signAndWrap({ futureOrderCancelTx: tx });
        }

        case 'freeze': {
            const FreezeTx = () => pb.FreezeTx;
            const tx = FreezeTx().create({
                base: base(),
                tokenAddr: txDesc.tokenAddr,
                targetAddr: txDesc.targetAddr,
                freeze: !!txDesc.freeze,
            });
            return signAndWrap({ freezeTx: tx });
        }

		case 'issue_token': {
			const IssueTokenTx = () => pb.IssueTokenTx;
			                                                                          
			const tx = IssueTokenTx().create({
				base: base(),
				totalSupply: String(txDesc.totalSupply || '0'),
				canMint: !!txDesc.canMint,
				canFreeze: !!txDesc.canFreeze,
				canBurn: !!txDesc.canBurn,
            });
            return signAndWrap({ issueTokenTx: tx });
        }

        case 'mint_token': {
            const MintTokenTx = () => pb.MintTokenTx;
            const tx = MintTokenTx().create({
                base: base(),
                tokenAddr: txDesc.tokenAddr,
                amount: String(txDesc.amount || ''),
                to: txDesc.to || '',
            });
            return signAndWrap({ mintTokenTx: tx });
        }

        case 'burn_token': {
            const BurnTokenTx = () => pb.BurnTokenTx;
            const tx = BurnTokenTx().create({
                base: base(),
                tokenAddr: txDesc.tokenAddr,
                amount: String(txDesc.amount || ''),
            });
            return signAndWrap({ burnTokenTx: tx });
        }

        case 'transfer_ownership': {
            const TransferOwnershipTx = () => pb.TransferOwnershipTx;
            const tx = TransferOwnershipTx().create({
                base: base(),
                tokenAddr: txDesc.tokenAddr,
                newOwner: txDesc.newOwner,
            });
            return signAndWrap({ transferOwnershipTx: tx });
        }

        case 'set_referrer': {
            const SetReferrerTx = () => pb.SetReferrerTx;
            const tx = SetReferrerTx().create({
                base: base(),
                referrerAddress: txDesc.referrerAddress,
            });
            return signAndWrap({ setReferrerTx: tx });
        }

        case 'witness_stake': {
            const WitnessStakeTx = () => pb.WitnessStakeTx;
            const OrderOp = pb.OrderOp;
            const tx = WitnessStakeTx().create({
                base: base(),
                op: txDesc.op === 'REMOVE' ? OrderOp.REMOVE : OrderOp.ADD,
                amount: String(txDesc.amount || '0'),
            });
            return signAndWrap({ witnessStakeTx: tx });
        }

        case 'miner': {
            const MinerTx = () => pb.MinerTx;
            const OrderOp = pb.OrderOp;
            const isRemove = txDesc.op === 'REMOVE';
            const rewardAddress = String(txDesc.reward_address || '').trim();
            const blsPublicKey = decodeOptionalBytes(txDesc.bls_public_key, 'bls_public_key');
            const blsProof = decodeOptionalBytes(txDesc.bls_proof, 'bls_proof');
            const endpoint = String(txDesc.endpoint || '').trim();
            const hasRegistration = blsPublicKey.length > 0 || blsProof.length > 0 || endpoint !== '';
            if (!isRemove && hasRegistration && (blsPublicKey.length === 0 || blsProof.length === 0 || endpoint === '')) {
                throw new Error('miner registration requires bls_public_key, bls_proof, and endpoint together');
            }
            const tx = MinerTx().create({
                base: base(),
                op: isRemove ? OrderOp.REMOVE : OrderOp.ADD,
                amount: String(txDesc.amount || '0'),
                rewardAddress,
                blsPublicKey,
                blsProof,
                endpoint,
            });
            return signAndWrap({ minerTx: tx });
        }

        case 'miner_info': {
            const MinerInfoTx = () => pb.MinerInfoTx;
            const tx = MinerInfoTx().create({
                base: base(),
                endpoint: String(txDesc.endpoint || '').trim(),
            });
            return signAndWrap({ minerInfoTx: tx });
        }

        case 'witness_claim_reward': {
            const WitnessClaimRewardTx = () => pb.WitnessClaimRewardTx;
            const tx = WitnessClaimRewardTx().create({ base: base() });
            return signAndWrap({ witnessClaimRewardTx: tx });
        }

        case 'witness_claim_stake': {
            const WitnessClaimStakeTx = () => pb.WitnessClaimStakeTx;
            const tx = WitnessClaimStakeTx().create({ base: base() });
            return signAndWrap({ witnessClaimStakeTx: tx });
        }

        default:
            throw new Error(`Unsupported tx type: ${txDesc.type}`);
    }
}

export function btcWithdrawalAuthorization(desc) {
    const version = desc.btcProtocolVersion ?? 0;
    if (version === 0) {
        if ((desc.btcMaxTotalFee ?? '') !== '' || (desc.btcMaxParts ?? 0) !== 0) throw new Error('BTC budgets require protocol version 3');
        return {};
    }
    if (version !== 3) throw new Error('Unsupported BTC protocol version');
    const fee = desc.btcMaxTotalFee;
    if (typeof fee !== 'string' || !/^[1-9]\d*$/.test(fee) || BigInt(fee) > (1n << 64n) - 1n) throw new Error('Invalid BTC total fee budget');
    const parts = desc.btcMaxParts;
    if (!Number.isInteger(parts) || parts < 1 || parts > 4096) throw new Error('BTC max parts must be between 1 and 4096');
    return { btcProtocolVersion: version, btcMaxTotalFee: fee, btcMaxParts: parts };
}


                                         
export function decodeAnyTx(bytes) {
    return AnyTx().decode(bytes);
}
