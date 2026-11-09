<script setup lang="ts">
import { ref, onMounted, computed, watch, nextTick } from 'vue'
import {
  DEFAULT_WALLET_ENV, WALLET_ENV_PROFILES,
  defaultEnvConfig, mergeWalletEnvConfigs, normalizeWalletEnv,
} from '../lib/env.js'
import { parsePrivateKeysText } from '../lib/private_key_import.js'
import { isValidRentStatus } from '../lib/rpc.js'
import {
  tokenDisplayLabel, tokenDisplayTitle, tokenDecimals as resolveTokenDecimals,
  toRawAmount, formatRawAmount, mergeBalanceTokenMetadata,
} from '../lib/asset_metadata.js'
import { top200IconForNative } from '../../shared/top200TokenIcons.mjs'

                                                                                                                                                                                                                                 
const DICT = {
  zh: {
    password: '密码', confirmPassword: '确认密码', enterPwd: '输入钱包密码', pwdPlaceholder: '至少8位', pwdConfirm: '再次输入',
    unlock: '解锁钱包', unlocking: '正在解锁…', orSep: '或', createOrImport: '创建 / 导入钱包',
    setupTitle: '设置钱包', tabCreate: '新建', tabImport: '导入',
    mnemonicEmpty: '点击下方按钮生成助记词', genMnemonic: '生成助记词',
    createWallet: '创建钱包', mnemonicWarn: '请务必抄写助记词，丢失后无法恢复',
    mnemonicLabel: '助记词（12个，空格分隔）', importWallet: '导入钱包',
    batchImportLabel: '使用私钥导入（每行一个）',
    batchImportHint: '支持 64 位 hex 或 0x 前缀的 32 字节 secp256k1 私钥及 # 注释。仅粘贴你自行保管的私钥',
    batchImportBtn: '导入私钥',
    switchAccount: '切换账户', copyAddr: '复制地址', settings: '设置', lockTitle: '锁定',
    manageAccounts: '账户', accountsTitle: '账户管理', accountListTitle: '钱包地址', currentAccount: '当前',
    deriveAccountTitle: '从现有助记词创建', deriveAccountHint: '使用同一助记词的下一个可用派生路径创建新地址。',
    addDerivedAccount: '＋ 创建新地址',
    privateKeyTitle: '导入私钥地址', privateKeyLabel: '32 字节 secp256k1 私钥（每行一个）',
    privateKeyHint: '支持 64 位 hex、0x 前缀、空行和 # 注释。私钥会使用当前钱包密码加密保存。',
    importPrivateKeys: '导入并切换', working: '处理中…', accountAdded: (addr: string) => `已创建新地址 ${addr}`,
    noNewAccounts: '这些私钥对应的地址已经存在于钱包中', accountActionFailed: (m: string) => `操作失败：${m}`,
    refreshing: '刷新中…', refreshBalance: '刷新余额',
    balanceLoadFailed: (m: string) => `余额读取失败：${m}`,
    balanceCachedAt: (time: string) => `显示上次余额 · ${time}`,
    balanceUpdatedAt: (time: string) => `更新于 ${time}`,
    send: '发送', history: '发送历史',
    sendTokenTitle: '发送代币', token: '代币', recvAddr: '接收地址', amount: '金额',
    feeUnit: '(最小单位 FB)', feeDefault: '(最小单位 FB，默认 1000)',
    nonce: 'Nonce', nonceHint: '默认使用当前节点返回的 nonce，可手动修改', nonceLoading: '读取中…', refreshNonce: '刷新 nonce',
    copyFullTxId: '复制完整 TxID', sending: '正在提交…', confirmSend: '确认发送',
    availableBalance: '可用余额', sentTitle: '交易已提交', sentSubtitle: '节点交易池已接收该交易，请等待链上确认。',
    sendAnother: '再发一笔', backWallet: '返回钱包', submittedTx: '已提交交易',
    localHistory: '本地发送历史', historyHint: '仅显示本钱包本地发送记录（当前账户）',
    loading: '加载中...', noHistory: '暂无发送记录',
    to: 'To:', copy: '复制', refresh: '刷新',
    explorerAddr: 'Explorer 地址', nodeAddr: '节点地址（转发用）', save: '保存',
    autoLock: '自动锁定时长（分钟）', autoLockHint: '无操作后自动锁定钱包，范围 1–1440 分钟',
    dangerOp: '危险操作 — 不可撤销', resetWallet: '重置钱包',
    approveFeeTitle: '设置交易手续费', source: '来源:', opType: '操作类型', signAccount: '签名账户',
    reject: '拒绝', next: '下一步 →', approveTitle: '确认签名',
    signAccountLbl: '签名账户:', feeLbl: 'Fee:', opDetail: '操作详情', approve: '批准签名',
    challengeStakeLbl: '挑战金', challengeTotalLbl: '合计锁定', fbBalanceLbl: '可花费 FB', balanceChecking: '余额读取中...',
    challengeBalanceOk: '扣除租金后的 FB 足够支付挑战金和手续费',
    challengeBalanceLow: (amt: string) => `可花费 FB 不足，至少需要 ${amt} FB`,
    errEnterPwd: '请输入密码', errPwdWrong: '密码错误',
    errGenFirst: '请先生成助记词', errPwdLen: '密码至少8位', errPwdMismatch: '两次密码不一致',
    errEnterMnemonic: '请输入助记词',
    errNoKeys: '未找到有效私钥，请按每行一个私钥的格式粘贴', errNoPwd: '请先输入钱包密码',
    importedN: (n: number) => `成功导入 ${n} 个账户`,
    importFailed: (m: string) => `导入失败：${m}`,
    errRecvAddr: '请输入接收地址', errAmount: '请输入有效金额', errToken: '请选择代币', errTokenPrecision: '该资产精度尚未确认，暂不能发送；余额按最小单位 raw 显示。', errAmountPrecision: (decimals: number) => `金额最多支持 ${decimals} 位小数`, errNonce: 'Nonce 必须是 0 到 18446744073709551615 的整数',
    txSubmitted: '交易已进入节点交易池', sendFailed: (m: string) => `发送失败：${m}`,
    txIdCopied: '已复制完整 TxID',
    confirmReset: '确定重置？助记词丢失将无法恢复！',
    loadHistoryFailed: '加载历史失败',
    approveFailed: '批准失败', rejectFailed: '拒绝失败', approving: '正在批准…', rejecting: '正在拒绝…', saving: '正在保存…',
  },
  en: {
    password: 'Password', confirmPassword: 'Confirm Password', enterPwd: 'Enter wallet password', pwdPlaceholder: 'Min 8 chars', pwdConfirm: 'Re-enter password',
    unlock: 'Unlock Wallet', unlocking: 'Unlocking…', orSep: 'or', createOrImport: 'Create / Import Wallet',
    setupTitle: 'Setup Wallet', tabCreate: 'New', tabImport: 'Import',
    mnemonicEmpty: 'Click below to generate mnemonic', genMnemonic: 'Generate Mnemonic',
    createWallet: 'Create Wallet', mnemonicWarn: 'Write down your mnemonic — it cannot be recovered if lost',
    mnemonicLabel: 'Mnemonic (12 words, space-separated)', importWallet: 'Import Wallet',
    batchImportLabel: 'Import with private keys (one per line)',
    batchImportHint: 'Accepts 32-byte secp256k1 keys as 64 hex characters or with a 0x prefix, plus # comments. Paste only keys you control',
    batchImportBtn: 'Import Private Keys',
    switchAccount: 'Switch account', copyAddr: 'Copy address', settings: 'Settings', lockTitle: 'Lock',
    manageAccounts: 'Accounts', accountsTitle: 'Manage Accounts', accountListTitle: 'Wallet Addresses', currentAccount: 'Current',
    deriveAccountTitle: 'Create from Existing Mnemonic', deriveAccountHint: 'Create a new address at the next unused derivation path of this wallet mnemonic.',
    addDerivedAccount: '＋ Create New Address',
    privateKeyTitle: 'Import Private-Key Address', privateKeyLabel: '32-byte secp256k1 private keys (one per line)',
    privateKeyHint: 'Supports 64-character hex, a 0x prefix, blank lines, and # comments. Keys are encrypted with the current wallet password.',
    importPrivateKeys: 'Import and Switch', working: 'Working…', accountAdded: (addr: string) => `Created address ${addr}`,
    noNewAccounts: 'The addresses for these private keys are already in the wallet', accountActionFailed: (m: string) => `Action failed: ${m}`,
    refreshing: 'Refreshing…', refreshBalance: 'Refresh Balance',
    balanceLoadFailed: (m: string) => `Balance unavailable: ${m}`,
    balanceCachedAt: (time: string) => `Last known balance · ${time}`,
    balanceUpdatedAt: (time: string) => `Updated ${time}`,
    send: 'Send', history: 'History',
    sendTokenTitle: 'Send Token', token: 'Token', recvAddr: 'Recipient', amount: 'Amount',
    feeUnit: '(min-unit FB)', feeDefault: '(min-unit FB, default 1000)',
    nonce: 'Nonce', nonceHint: 'Defaults to the nonce returned by the current node and can be edited', nonceLoading: 'Loading…', refreshNonce: 'Refresh nonce',
    copyFullTxId: 'Copy Full TxID', sending: 'Submitting…', confirmSend: 'Confirm Send',
    availableBalance: 'Available balance', sentTitle: 'Transaction submitted', sentSubtitle: 'The node pool accepted this transaction. Wait for on-chain confirmation.',
    sendAnother: 'Send another', backWallet: 'Back to wallet', submittedTx: 'Submitted transaction',
    localHistory: 'Local Send History', historyHint: 'Only local send records of this wallet (current account)',
    loading: 'Loading...', noHistory: 'No records',
    to: 'To:', copy: 'Copy', refresh: 'Refresh',
    explorerAddr: 'Explorer URL', nodeAddr: 'Node Address (for forwarding)', save: 'Save',
    autoLock: 'Auto-lock timeout (minutes)', autoLockHint: 'Auto-lock after inactivity. Range: 1–1440 minutes',
    dangerOp: 'Danger — irreversible', resetWallet: 'Reset Wallet',
    approveFeeTitle: 'Set Transaction Fee', source: 'From:', opType: 'Operation', signAccount: 'Signing Account',
    reject: 'Reject', next: 'Next →', approveTitle: 'Confirm Signature',
    signAccountLbl: 'Signer:', feeLbl: 'Fee:', opDetail: 'Details', approve: 'Approve',
    challengeStakeLbl: 'Challenge Stake', challengeTotalLbl: 'Total Required', fbBalanceLbl: 'Spendable FB', balanceChecking: 'Checking balance...',
    challengeBalanceOk: 'Spendable FB after rent covers the challenge stake and fee',
    challengeBalanceLow: (amt: string) => `Spendable FB is below the required ${amt} FB`,
    errEnterPwd: 'Please enter password', errPwdWrong: 'Wrong password',
    errGenFirst: 'Generate mnemonic first', errPwdLen: 'Password must be 8+ chars', errPwdMismatch: 'Passwords do not match',
    errEnterMnemonic: 'Please enter mnemonic',
    errNoKeys: 'No valid keys found. Paste one private key per line', errNoPwd: 'Enter wallet password first',
    importedN: (n: number) => `Imported ${n} account(s)`,
    importFailed: (m: string) => `Import failed: ${m}`,
    errRecvAddr: 'Please enter recipient address', errAmount: 'Please enter a valid amount', errToken: 'Please select a token', errTokenPrecision: 'Asset precision is not confirmed. Sending is unavailable; balances show raw smallest units.', errAmountPrecision: (decimals: number) => `Amount supports at most ${decimals} decimal places`, errNonce: 'Nonce must be an integer from 0 to 18446744073709551615',
    txSubmitted: 'Transaction admitted to the node pool', sendFailed: (m: string) => `Send failed: ${m}`,
    txIdCopied: 'Full TxID copied',
    confirmReset: 'Reset wallet? Mnemonic loss is unrecoverable!',
    loadHistoryFailed: 'Failed to load history',
    approveFailed: 'Approve Failed', rejectFailed: 'Reject Failed', approving: 'Approving…', rejecting: 'Rejecting…', saving: 'Saving…',
  },
} as const

type Lang = 'zh' | 'en'
const LANG_KEY = 'frostbit_wallet_lang'
const initLang: Lang = (localStorage.getItem(LANG_KEY) as Lang) || (navigator.language.toLowerCase().startsWith('zh') ? 'zh' : 'en')
const lang = ref<Lang>(initLang)
const t = computed(() => DICT[lang.value])
function toggleLang() {
  lang.value = lang.value === 'zh' ? 'en' : 'zh'
  localStorage.setItem(LANG_KEY, lang.value)
  unlockBtnText.value = t.value.unlock
}

                                                                                                                                                                                                               
function bg(msg: any): Promise<any> {
  return new Promise((resolve, reject) => {
    chrome.runtime.sendMessage(msg, (res: any) => {
      if (chrome.runtime.lastError) return reject(new Error(chrome.runtime.lastError.message))
      if (res?.error) return reject(new Error(res.error))
      resolve(res)
    })
  })
}

                                                                                                                                                                                                                             
type Page = 'lock' | 'setup' | 'main' | 'accounts' | 'send' | 'history' | 'settings' | 'approve-fee' | 'approve'
const page = ref<Page>('lock')
const setupTab = ref<'create' | 'import'>('create')
const isApprovalPage = computed(() => page.value === 'approve-fee' || page.value === 'approve')
const isApprovalWindow = typeof window !== 'undefined'
  && new URLSearchParams(window.location.search).get('mode') === 'approval'
const isApprovalLayout = computed(() => isApprovalWindow || isApprovalPage.value)

watch(isApprovalLayout, (active) => {
  document.body.classList.toggle('approval-window', active)
}, { immediate: true })

                                                                                                                                                                                                                             
const address = ref('')
const accounts = ref<string[]>([])
const balances = ref<Record<string, any>>({})
const approveBalances = ref<Record<string, any>>({})
const rentStatus = ref<Record<string, any> | null>(null)
const approveRentStatus = ref<Record<string, any> | null>(null)
const loadingApproveBalance = ref(false)
const approveBalanceError = ref('')
const loadingBalance = ref(false)
const balanceError = ref('')
const balanceUpdatedAt = ref(0)
const balanceCached = ref(false)
const balanceMainRef = ref<HTMLElement | null>(null)
const balanceValueRef = ref<HTMLElement | null>(null)
const balanceFontSize = ref(34)
const generatedMnemonic = ref('')
const showAccountList = ref(false)
const hasMnemonic = ref(false)

                                                                                                                                                                                                                             
const unlockPassword = ref('')
const unlockBtnText = ref('')
const unlockShake = ref(false)
const unlockLoading = ref(false)

function shakeUnlockBtn(msg: string) {
  unlockBtnText.value = msg
  unlockShake.value = false
  requestAnimationFrame(() => { unlockShake.value = true })
  setTimeout(() => { unlockShake.value = false }, 500)
  setTimeout(() => { if (unlockBtnText.value === msg) unlockBtnText.value = t.value.unlock }, 2000)
}

const createPassword = ref('')
const createPassword2 = ref('')
const setupError = ref('')

const importMnemonic = ref('')
const importPassword = ref('')
const importFileStatus = ref('')
const importFileError = ref('')
const importKeysText = ref('')
const accountPrivateKeysText = ref('')
const accountActionStatus = ref('')
const accountActionError = ref('')
const accountActionLoading = ref(false)
const setupLoading = ref(false)

const sendToken = ref('FB')
const sendTo = ref('')
const sendAmount = ref('')
const sendStatus = ref('')
const sendError = ref('')
const sendTxId = ref('')
const sendFee = ref('1000')
const sendNonce = ref('')
const sendNonceLoading = ref(false)
const sendNonceError = ref('')
const approveFee = ref('1000')
const approveAccountIdx = ref(-1)                                  
const sendLoading = ref(false)
const sendReceipt = ref<null | {
  txId: string
  to: string
  token: string
  amount: string
  nonce: string
  fee: string
}>(null)
const approvalAction = ref<'approve' | 'reject' | ''>('')
const settingsSaving = ref(false)
const txHistory = ref<any[]>([])
const txHistoryLoading = ref(false)
const txHistoryError = ref('')
let accountDataSeq = 0
let approveBalanceSeq = 0

type WalletEnv = 'dev' | 'test' | 'prod'
type WalletEnvConfig = { explorerUrl: string; nodeAddr: string }
type WalletEnvConfigs = Record<WalletEnv, WalletEnvConfig>

const walletEnv = ref<WalletEnv>(normalizeWalletEnv(DEFAULT_WALLET_ENV) as WalletEnv)
const envConfigs = ref<WalletEnvConfigs>(mergeWalletEnvConfigs() as WalletEnvConfigs)
const explorerUrl = ref(defaultEnvConfig(DEFAULT_WALLET_ENV).explorerUrl)
const nodeAddr = ref('')
const autoLockMinutes = ref(30)

const pendingRequests = ref<any[]>([])
const approveError = ref('')

                                                        
const TX_TYPES = new Set([
  'transaction', 'order', 'issue_token', 'mint_token', 'burn_token',
  'transfer_ownership', 'freeze', 'miner', 'witness_stake',
  'witness_vote', 'witness_challenge', 'arbitration_vote', 'recharge_request',
  'frost_withdraw', 'frost_withdraw_cancel', 'future_order', 'future_add_margin', 'future_remove_margin',
  'future_order_cancel', 'set_referrer', 'witness_claim_reward', 'witness_claim_stake', 'miner_info',
])

function isTxType(type: string) { return TX_TYPES.has(type) }
function findAccountIdx(list: string[], addr: string) { const i = list.indexOf(addr); return i >= 0 ? i : 0 }
function normalizeRawAmount(raw: string | number | undefined | null) {
  const s = String(raw ?? '').trim()
  return /^\d+$/.test(s) ? s : '0'
}
function addRawAmounts(...values: Array<string | number | undefined | null>) {
  return values.reduce((sum, value) => sum + BigInt(normalizeRawAmount(value)), 0n).toString()
}
function gteRawAmount(left: string, right: string) {
  return BigInt(normalizeRawAmount(left)) >= BigInt(normalizeRawAmount(right))
}

const RENT_ACTIVATION_FEE_RAW = '1000000'
const sendRentActivationHint = computed(() => {
  const token = String(sendToken.value || '').trim()
  const to = sendTo.value.trim().toLowerCase()
  if (!token || token === 'FB' || !to.startsWith('fbc1')) return ''
  const amount = fmtAmt(RENT_ACTIVATION_FEE_RAW, tokenDecimals('FB'))
  return ui(
    `如果接收方尚无名义 FB，本次转账还会额外支出 ${amount} FB 作为账户激活租金。`,
    `If the recipient has no nominal FB, this transfer also spends ${amount} FB as account activation rent balance.`,
  )
})

function resetAccountScopedData() {
  balances.value = {}
  rentStatus.value = null
  approveRentStatus.value = null
  approveBalances.value = {}
  approveBalanceSeq += 1
  balanceError.value = ''
  balanceUpdatedAt.value = 0
  balanceCached.value = false
  txHistory.value = []
  txHistoryError.value = ''
  sendError.value = ''
  sendStatus.value = ''
  sendTxId.value = ''
  sendNonce.value = ''
  sendNonceError.value = ''
  sendNonceLoading.value = false
  sendReceipt.value = null
}

function beginAccountDataLoad() {
  accountDataSeq += 1
  resetAccountScopedData()
  return accountDataSeq
}

function isCurrentAccountDataLoad(seq: number, addr: string) {
  return seq === accountDataSeq && address.value === addr
}

function applyBalanceSnapshot(snapshot: any, cached: boolean) {
  if (!isValidRentStatus(snapshot?.rent_status)) throw new Error(ui('租金余额暂不可用，请刷新余额', 'Rent balance unavailable. Refresh balances.'))
  const next = snapshot?.balances && typeof snapshot.balances === 'object' ? snapshot.balances : {}
  balances.value = mergeBalanceTokenMetadata(next, balances.value)
  rentStatus.value = snapshot.rent_status
  balanceUpdatedAt.value = Number(snapshot?.updatedAt) || 0
  balanceCached.value = cached
  normalizeSendToken()
}

async function loadCachedBalances(seq: number, targetAddress: string) {
  try {
    const cached = await bg({ type: 'GET_CACHED_BALANCES', address: targetAddress })
    if (isCurrentAccountDataLoad(seq, targetAddress) && cached.updatedAt) applyBalanceSnapshot(cached, true)
  } catch { }
}

async function loadBalanceMetadata(seq: number, targetAddress: string) {
  try {
    const enriched = await bg({ type: 'GET_BALANCE_METADATA', address: targetAddress })
    if (isCurrentAccountDataLoad(seq, targetAddress) && enriched.updatedAt === balanceUpdatedAt.value) {
      applyBalanceSnapshot(enriched, balanceCached.value)
    }
  } catch { }
}

async function refreshBalances(seq = accountDataSeq, targetAddress = address.value) {
  if (!targetAddress) return
  loadingBalance.value = true
  balanceError.value = ''
  try {
    const fresh = await bg({ type: 'GET_BALANCES_FAST', address: targetAddress })
    if (!isCurrentAccountDataLoad(seq, targetAddress)) return
    applyBalanceSnapshot(fresh, false)
    void loadBalanceMetadata(seq, targetAddress)
  } catch (e: any) {
    if (isCurrentAccountDataLoad(seq, targetAddress)) balanceError.value = e?.message || String(e)
  } finally {
    if (isCurrentAccountDataLoad(seq, targetAddress)) loadingBalance.value = false
  }
}

const currentPendingRequest = computed(() => pendingRequests.value[0] ?? null)
const currentTxDesc = computed(() => currentPendingRequest.value?.txDesc ?? null)
const approvalSubtitle = computed(() => {
  const requestType = String(currentPendingRequest.value?.type || '')
  if (requestType === 'REQUEST_ACCOUNTS') {
    return ui(
      '允许此站点查看当前账户地址。授权会在钱包锁定时自动撤销。',
      'Allow this site to view the current account address. Access is revoked when the wallet locks.',
    )
  }
  if (requestType === 'SIGN_MESSAGE') {
    return ui(
      '请核对消息原文。批准后钱包会生成签名。',
      'Review the exact message. Approval creates a wallet signature.',
    )
  }
  if (requestType === 'SIGN_TX') {
    return ui(
      '请核对交易内容。批准后只返回签名交易，不会由钱包广播。',
      'Review the transaction. Approval returns a signed transaction without broadcasting it.',
    )
  }
  return ui(
    '请核对交易内容。批准后交易会提交到链上。',
    'Review the transaction. Approval submits it to the network.',
  )
})
const approveSignerAddress = computed(() => {
  if (approveAccountIdx.value >= 0 && approveAccountIdx.value < accounts.value.length) return accounts.value[approveAccountIdx.value]
  return address.value
})
const isWitnessChallengeRequest = computed(() => currentTxDesc.value?.type === 'witness_challenge')
const challengeStakeRaw = computed(() => isWitnessChallengeRequest.value ? normalizeRawAmount(currentTxDesc.value?.stakeAmount) : '0')
const challengeFeeRaw = computed(() => isWitnessChallengeRequest.value ? normalizeRawAmount(approveFee.value || '0') : '0')
const challengeTotalRaw = computed(() => isWitnessChallengeRequest.value ? addRawAmounts(challengeStakeRaw.value, challengeFeeRaw.value) : '0')
const challengeStakeDisplay = computed(() => fmtAmt(challengeStakeRaw.value))
const challengeFeeDisplay = computed(() => fmtAmt(challengeFeeRaw.value))
const challengeTotalDisplay = computed(() => fmtAmt(challengeTotalRaw.value))
const approveSignerFbRaw = computed(() => normalizeRawAmount(approveRentStatus.value?.spendable_fb))
const approveSignerFbDisplay = computed(() => isValidRentStatus(approveRentStatus.value) ? fmtAmt(approveSignerFbRaw.value) : '—')
const challengeBalanceEnough = computed(() => !isWitnessChallengeRequest.value || gteRawAmount(approveSignerFbRaw.value, challengeTotalRaw.value))
const approveBalanceCheckError = computed(() => {
  if (!isTxType(currentTxDesc.value?.type || '')) return ''
  if (loadingApproveBalance.value) return t.value.balanceChecking
  if (approveBalanceError.value) return approveBalanceError.value
  return transactionBalanceError(currentTxDesc.value, approveFee.value || '1000', approveRentStatus.value, approveBalances.value)
})

function ui(zh: string, en: string) { return lang.value === 'zh' ? zh : en }

function envLabel(env: string) {
  if (env === 'test') return ui('测试', 'Test')
  if (env === 'prod') return ui('正式', 'Prod')
  return ui('开发', 'Dev')
}

function readEnvConfigs(raw: any): WalletEnvConfigs {
  return mergeWalletEnvConfigs(raw) as WalletEnvConfigs
}

function exportEnvConfigs(): WalletEnvConfigs {
  return JSON.parse(JSON.stringify(envConfigs.value)) as WalletEnvConfigs
}

function rememberVisibleEnvConfig() {
  envConfigs.value = {
    ...envConfigs.value,
    [walletEnv.value]: {
      explorerUrl: explorerUrl.value.trim() || defaultEnvConfig(walletEnv.value).explorerUrl,
      nodeAddr: nodeAddr.value.trim(),
    },
  } as WalletEnvConfigs
}

function selectWalletEnv(env: string) {
  rememberVisibleEnvConfig()
  const next = normalizeWalletEnv(env) as WalletEnv
  walletEnv.value = next
  const cfg = envConfigs.value[next] || defaultEnvConfig(next)
  explorerUrl.value = cfg.explorerUrl
  nodeAddr.value = cfg.nodeAddr || ''
}

async function loadSettingsState() {
  const res = await bg({ type: 'GET_SETTINGS' })
  const nextEnv = normalizeWalletEnv(res.walletEnv) as WalletEnv
  const configs = readEnvConfigs(res.envConfigs)
  walletEnv.value = nextEnv
  envConfigs.value = configs
  explorerUrl.value = res.explorerUrl ?? configs[nextEnv].explorerUrl
  nodeAddr.value = res.nodeAddr ?? configs[nextEnv].nodeAddr
  autoLockMinutes.value = Number(res.autoLockMinutes) || 30
}

const explorerPlaceholder = computed(() => defaultEnvConfig(walletEnv.value).explorerUrl)
const nodePlaceholder = computed(() => walletEnv.value === 'dev' ? '127.0.0.1:6000' : ui('留空自动选择节点', 'Auto-select node'))

function txTypeTitle(type: string): string {
  const map: Record<string, [string, string]> = {
    transaction: ['转账', 'Transfer'],
    order: ['现货委托', 'Spot Order'],
    future_order: ['永续委托', 'Perp Order'],
    future_order_cancel: ['取消永续委托', 'Cancel Perp Order'],
    future_add_margin: ['追加保证金', 'Add Margin'],
    future_remove_margin: ['提取保证金', 'Remove Margin'],
    witness_vote: ['见证投票', 'Witness Vote'],
    witness_challenge: ['见证挑战', 'Witness Challenge'],
    arbitration_vote: ['仲裁投票', 'Arbitration Vote'],
    recharge_request: ['充值上账请求', 'Recharge Request'],
    frost_withdraw: ['提现请求', 'Withdraw Request'],
    frost_withdraw_cancel: ['取消提现', 'Cancel Withdrawal'],
    withdraw_request: ['提现请求', 'Withdraw Request'],
    issue_token: ['发行代币', 'Issue Token'],
    mint_token: ['增发代币', 'Mint Token'],
    burn_token: ['销毁代币', 'Burn Token'],
    set_referrer: ['设置邀请人', 'Set Referrer'],
    connect: ['连接站点', 'Connect Site'],
    sign_message: ['签名消息', 'Sign Message'],
  }
  const item = map[type]
  return item ? ui(item[0], item[1]) : type
}

function rawDisplay(v: any, fallback = '-', decimals: number | null = TOKEN_DECIMALS) {
  const s = String(v ?? '').trim()
  return /^\d+$/.test(s) ? fmtAmt(s, decimals) : (s || fallback)
}

function feeRawDisplay(v: any) {
  const raw = normalizeRawAmount(v || '1000')
  return ui(`${raw} 最小单位 (${fmtAmt(raw)} FB)`, `${raw} min-unit (${fmtAmt(raw)} FB)`)
}

const approvalSummaryRows = computed(() => {
  const tx = currentTxDesc.value || {}
  const type = String(tx.type || '')
  const amountDecimals = tokenDecimals(String(tx.tokenAddress || tx.asset || ''))
  const rows: Array<{ label: string; value: string; title?: string }> = [
    { label: ui('操作', 'Operation'), value: txTypeTitle(type) },
  ]
  if (tx.pair) rows.push({ label: ui('交易对', 'Pair'), value: String(tx.pair).replace('_', '/') })
  if (tx.side) rows.push({ label: ui('方向', 'Side'), value: String(tx.side) })
  if (tx.price) rows.push({ label: ui('价格', 'Price'), value: rawDisplay(tx.price) })
  if (tx.amount) rows.push({ label: ui('数量/金额', 'Amount'), value: rawDisplay(tx.amount, '-', amountDecimals) })
  if (tx.leverage) rows.push({ label: ui('杠杆', 'Leverage'), value: `${tx.leverage}x` })
  if (tx.to) rows.push({ label: ui('接收方', 'Recipient'), value: String(tx.to) })
  const amountToken = String(tx.tokenAddress || tx.asset || '')
  if (amountToken) rows.push({ label: ui('代币', 'Token'), value: tokenLabel(amountToken), title: amountToken })
  if (tx.positionId) rows.push({ label: ui('仓位', 'Position'), value: String(tx.positionId) })
  if (tx.orderId || tx.opTargetId) rows.push({ label: ui('订单', 'Order'), value: String(tx.orderId || tx.opTargetId) })
  if (tx.withdrawId) rows.push({ label: ui('提现请求 ID', 'Withdrawal Request ID'), value: String(tx.withdrawId) })
  if (type === 'frost_withdraw' && tx.btcProtocolVersion) {
    rows.push({ label: ui('BTC 提现协议', 'BTC withdrawal protocol'), value: String(tx.btcProtocolVersion) })
    rows.push({ label: ui('BTC 总矿工费上限', 'BTC total network fee cap'), value: `${String(tx.btcMaxTotalFee || '')} sat` })
    rows.push({ label: ui('最多付款笔数', 'Maximum payment transactions'), value: String(tx.btcMaxParts || '') })
  }
  if (tx.toAddress) rows.push({ label: ui('接收方', 'Recipient'), value: String(tx.toAddress) })
  if (tx.originalRequestTx) rows.push({ label: ui('历史请求证明', 'Legacy Request Proof'), value: ui('已附带', 'Included') })
  if (tx.closePositionId) rows.push({ label: ui('平仓仓位', 'Close Position'), value: String(tx.closePositionId) })
  if (type === 'arbitration_vote') {
    rows.push({ label: ui('挑战 ID', 'Challenge ID'), value: String(tx.challengeId || '-') })
    rows.push({ label: ui('充值请求 ID', 'Deposit Request ID'), value: String(tx.requestId || '-') })
    rows.push({ label: ui('本轮仲裁轮号', 'Arbitration Ballot'), value: String(tx.ballotRound || '-') })
    const direction = tx.vote === 'PASS' ? ui('充值有效', 'Valid deposit') : tx.vote === 'FAIL' ? ui('充值无效', 'Invalid deposit') : tx.vote === 'ABSTAIN' ? ui('弃权', 'Abstain') : String(tx.vote || '-')
    rows.push({ label: ui('裁决方向', 'Verdict'), value: direction })
  }
  rows.push({ label: ui('签名账户', 'Signer'), value: approveSignerAddress.value || address.value || '-' })
  if (isTxType(type)) rows.push({ label: ui('手续费', 'Fee'), value: feeRawDisplay(approveFee.value) })
  return rows
})

async function loadApproveBalances() {
  const seq = ++approveBalanceSeq
  const signer = approveSignerAddress.value
  approveRentStatus.value = null
  approveBalances.value = {}
  if (!signer || !currentPendingRequest.value || !isTxType(currentTxDesc.value?.type || '')) {
    approveBalances.value = {}
    approveBalanceError.value = ''
    return
  }
  loadingApproveBalance.value = true
  approveBalanceError.value = ''
  try {
    const bal = await bg({ type: 'GET_BALANCES', address: signer })
    if (seq !== approveBalanceSeq) return
    if (!isValidRentStatus(bal.rent_status)) throw new Error(ui('租金余额暂不可用，请刷新余额', 'Rent balance unavailable. Refresh balances.'))
    approveBalances.value = bal.balances ?? {}
    approveRentStatus.value = bal.rent_status
  } catch (e: any) {
    if (seq !== approveBalanceSeq) return
    approveBalances.value = {}
    approveRentStatus.value = null
    approveBalanceError.value = e.message || ''
  } finally {
    if (seq === approveBalanceSeq) loadingApproveBalance.value = false
  }
}

watch([approveSignerAddress, currentPendingRequest], () => {
  void loadApproveBalances()
})

async function checkPendingRequests() {
  const res = await bg({ type: 'GET_PENDING_REQUESTS' })
  pendingRequests.value = res.requests || []
  approveError.value = ''
  if (pendingRequests.value.length > 0) {
    const reqType = pendingRequests.value[0]?.txDesc?.type || ''
    approveFee.value = '1000'
    approveAccountIdx.value = findAccountIdx(accounts.value, address.value)
    page.value = isTxType(reqType) ? 'approve-fee' : 'approve'
  }
}

function confirmFeeAndContinue() {
  if (approveBalanceCheckError.value) return
  page.value = 'approve'
}


                                                                                                                                                                                                                             
onMounted(async () => {
  unlockBtnText.value = t.value.unlock
  try { await loadSettingsState() } catch { }
  try {
    const { has } = await bg({ type: 'HAS_WALLET' })
    if (!has) { page.value = 'setup'; return }
    const { unlocked } = await bg({ type: 'IS_UNLOCKED' })
    if (unlocked) {
      page.value = 'main'
      await loadMain()
      await checkPendingRequests()
      if (page.value !== 'approve' && page.value !== 'approve-fee') page.value = 'main'
    }
    else page.value = 'lock'
  } catch { page.value = 'lock' }
})

                                                                                                                                                                                                                                
async function unlock() {
  if (unlockLoading.value) return
  if (!unlockPassword.value) { shakeUnlockBtn(t.value.errEnterPwd); return }
  unlockLoading.value = true
  unlockBtnText.value = t.value.unlocking
  try {
    const res = await bg({ type: 'UNLOCK', password: unlockPassword.value })
    address.value = res.address
    unlockPassword.value = ''
    unlockBtnText.value = t.value.unlock
    page.value = 'main'
    await loadMain()
    await checkPendingRequests()
    if (page.value !== 'approve' && page.value !== 'approve-fee') page.value = 'main'
  } catch (e: any) {
    shakeUnlockBtn(e.message === '密码错误' ? t.value.errPwdWrong : (e.message || t.value.errPwdWrong))
  } finally {
    unlockLoading.value = false
    if (!unlockShake.value) unlockBtnText.value = t.value.unlock
  }
}

                                                                                                                                                                                                                             
async function generateMnemonic() {
  const res = await bg({ type: 'GENERATE_MNEMONIC' })
  generatedMnemonic.value = res.mnemonic
}

async function createWallet() {
  if (setupLoading.value) return
  setupError.value = ''
  if (!generatedMnemonic.value) return (setupError.value = t.value.errGenFirst)
  if (createPassword.value.length < 8) return (setupError.value = t.value.errPwdLen)
  if (createPassword.value !== createPassword2.value) return (setupError.value = t.value.errPwdMismatch)
  setupLoading.value = true
  try {
    const res = await bg({ type: 'CREATE_WALLET', mnemonic: generatedMnemonic.value, password: createPassword.value })
    address.value = res.address
    page.value = 'main'
    await loadMain()
    await checkPendingRequests()
    if (page.value !== 'approve' && page.value !== 'approve-fee') page.value = 'main'
  } catch (e: any) {
    setupError.value = e.message
  } finally {
    setupLoading.value = false
  }
}

async function importWallet() {
  if (setupLoading.value) return
  setupError.value = ''
  if (!importMnemonic.value.trim()) return (setupError.value = t.value.errEnterMnemonic)
  if (importPassword.value.length < 8) return (setupError.value = t.value.errPwdLen)
  setupLoading.value = true
  try {
    const res = await bg({ type: 'IMPORT_WALLET', mnemonic: importMnemonic.value.trim(), password: importPassword.value })
    address.value = res.address
    page.value = 'main'
    await loadMain()
    await checkPendingRequests()
    if (page.value !== 'approve' && page.value !== 'approve-fee') page.value = 'main'
  } catch (e: any) {
    setupError.value = e.message
  } finally {
    setupLoading.value = false
  }
}

                                                                                                                                                                                                                             
async function loadMain() {
  const seq = beginAccountDataLoad()
  loadingBalance.value = true
  try {
    const res = await bg({ type: 'GET_ACCOUNTS' })
    if (seq !== accountDataSeq) return
    accounts.value = res.accounts ?? []
    hasMnemonic.value = !!res.hasMnemonic
                                                                                                
    if (!address.value || !accounts.value.includes(address.value))
      address.value = (res.selectedAddress && accounts.value.includes(res.selectedAddress))
        ? res.selectedAddress : (accounts.value[0] ?? '')
    const targetAddress = address.value
    if (!targetAddress) { loadingBalance.value = false; return }
    await loadCachedBalances(seq, targetAddress)
    if (!isCurrentAccountDataLoad(seq, targetAddress)) return
    void refreshBalances(seq, targetAddress)
    if (page.value === 'history') {
      await loadLocalTxHistory()
    }
  } catch (e: any) {
    if (e?.message?.includes('locked')) { page.value = 'lock'; return }
    if (seq === accountDataSeq) {
      balanceError.value = e?.message || String(e)
      loadingBalance.value = false
    }
  }
}

async function selectAccount(index: number) {
  const targetAddress = accounts.value[index]
  if (!targetAddress) return
  const seq = beginAccountDataLoad()
  address.value = targetAddress
  showAccountList.value = false
  loadingBalance.value = true
  try {
    await bg({ type: 'SELECT_ACCOUNT', index })
    await loadCachedBalances(seq, targetAddress)
    if (!isCurrentAccountDataLoad(seq, targetAddress)) return
    void refreshBalances(seq, targetAddress)
    if (page.value === 'history') {
      await loadLocalTxHistory()
    }
  } catch (e: any) {
    if (isCurrentAccountDataLoad(seq, targetAddress)) {
      balanceError.value = e?.message || String(e)
      loadingBalance.value = false
    }
  }
}

                                                                                                                                                                                                           
async function importAccountsFromText() {
  if (setupLoading.value) return
  importFileStatus.value = ''
  importFileError.value = ''
  let hexKeys: string[]
  try { hexKeys = parsePrivateKeysText(importKeysText.value) }
  catch (e: any) { importFileError.value = e.message || t.value.errNoKeys; return }
  if (importPassword.value.length < 8) { importFileError.value = t.value.errPwdLen; return }
  setupLoading.value = true
  try {
    const res = await bg({ type: 'IMPORT_ACCOUNTS_FILE', hexKeys, password: importPassword.value })
    importKeysText.value = ''
    importFileStatus.value = t.value.importedN(res.added.length)
    await loadMain()
    page.value = 'main'
  } catch (e: any) {
    importFileError.value = t.value.importFailed(e.message)
  } finally {
    setupLoading.value = false
  }
}

function openAccountsPage() {
  showAccountList.value = false
  accountActionStatus.value = ''
  accountActionError.value = ''
  page.value = 'accounts'
}

async function toggleAccountList() {
  showAccountList.value = !showAccountList.value
  if (!showAccountList.value) return
  await nextTick()
  const list = document.querySelector<HTMLElement>('.account-list')
  const active = list?.querySelector<HTMLElement>('.account-item-active')
  if (!list || !active) return
  list.scrollTop = Math.max(0, active.offsetTop - (list.clientHeight - active.clientHeight) / 2)
}

async function addDerivedAccount() {
  accountActionStatus.value = ''
  accountActionError.value = ''
  accountActionLoading.value = true
  try {
    const res = await bg({ type: 'ADD_ACCOUNT' })
    address.value = res.address
    await loadMain()
    accountActionStatus.value = t.value.accountAdded(shortAddr(res.address))
  } catch (e: any) {
    accountActionError.value = t.value.accountActionFailed(e.message)
  } finally {
    accountActionLoading.value = false
  }
}

async function importPrivateKeysUnlocked() {
  accountActionStatus.value = ''
  accountActionError.value = ''
  let hexKeys: string[]
  try { hexKeys = parsePrivateKeysText(accountPrivateKeysText.value) }
  catch (e: any) { accountActionError.value = e.message || t.value.errNoKeys; return }

  accountActionLoading.value = true
  try {
    const res = await bg({ type: 'IMPORT_PRIVATE_KEYS', hexKeys })
    accountPrivateKeysText.value = ''
    if (res.selectedAddress) address.value = res.selectedAddress
    await loadMain()
    accountActionStatus.value = res.added.length
      ? t.value.importedN(res.added.length)
      : t.value.noNewAccounts
  } catch (e: any) {
    accountActionError.value = t.value.accountActionFailed(e.message)
  } finally {
    accountActionLoading.value = false
  }
}

async function lock() {
  await bg({ type: 'LOCK' })
  accountDataSeq += 1
  address.value = ''
  accounts.value = []
  hasMnemonic.value = false
  resetAccountScopedData()
  loadingBalance.value = false
  txHistoryLoading.value = false
  page.value = 'lock'
}

                                                                                                                                                                                                                                
async function confirmSend() {
  if (sendLoading.value || sendReceipt.value) return
  sendError.value = ''
  sendStatus.value = ''
  sendTxId.value = ''
  if (!sendTo.value.trim()) return (sendError.value = t.value.errRecvAddr)
  if (sendAmountError.value) return (sendError.value = sendAmountError.value)
  let amountRaw: string
  try {
    amountRaw = toRawAmt(sendAmount.value, tokenDecimals(sendToken.value))
  } catch {
    return (sendError.value = t.value.errAmount)
  }
  if (!amountRaw || BigInt(amountRaw) <= 0n) return (sendError.value = t.value.errAmount)
  if (!sendToken.value) return (sendError.value = t.value.errToken)
  if (sendBalanceError.value) return (sendError.value = sendBalanceError.value)
  const nonce = sendNonce.value.trim()
  if (!/^\d+$/.test(nonce) || BigInt(nonce) > 0xffffffffffffffffn) return (sendError.value = t.value.errNonce)
  sendLoading.value = true
  try {
    const res = await bg({
      type: 'SEND_TX',
      fee: sendFee.value || '1000',
      nonce,
      txDesc: {
        type: 'transaction',
        to: sendTo.value.trim(),
        tokenAddress: sendToken.value,
        amount: amountRaw,
      },
    })
    const txId = res.txId || ''
    sendTxId.value = txId
    sendReceipt.value = {
      txId,
      to: sendTo.value.trim(),
      token: sendToken.value,
      amount: sendAmount.value.trim(),
      nonce,
      fee: sendFee.value || '1000',
    }
    sendStatus.value = t.value.txSubmitted
    await loadLocalTxHistory()
  } catch (e: any) {
    sendError.value = t.value.sendFailed(e.message)
  } finally {
    sendLoading.value = false
  }
}

                                                                                                                                                                                                                       
async function approveRequest(id: number) {
  if (approvalAction.value) return
  if (approveBalanceCheckError.value) { approveError.value = approveBalanceCheckError.value; return }
  approveError.value = ''
  approvalAction.value = 'approve'
  try {
    await bg({ type: 'APPROVE_REQUEST', id, fee: approveFee.value || '1000', accountIndex: approveAccountIdx.value })
    await checkPendingRequests()
    if (pendingRequests.value.length === 0) window.close()
  } catch (e: any) { approveError.value = `${t.value.approveFailed}: ${friendlyApprovalError(e.message)}` }
  finally { approvalAction.value = '' }
}

async function rejectRequest(id: number) {
  if (approvalAction.value) return
  approveError.value = ''
  approvalAction.value = 'reject'
  try {
    await bg({ type: 'REJECT_REQUEST', id })
    await checkPendingRequests()
    if (pendingRequests.value.length === 0) window.close()
  } catch (e: any) { approveError.value = `${t.value.rejectFailed}: ${friendlyApprovalError(e.message)}` }
  finally { approvalAction.value = '' }
}

function friendlyApprovalError(raw: any): string {
  const msg = String(raw || '')
  const lower = msg.toLowerCase()
  if (lower.includes('locked')) return ui('钱包已锁定，请先解锁。', 'Wallet is locked. Unlock first.')
  if (lower.includes('insufficient') || lower.includes('balance') || lower.includes('margin')) return ui('余额或保证金不足，请返回页面调整金额。', 'Insufficient balance or margin. Adjust the amount in the page.')
  if (lower.includes('nonce')) return ui('账户 nonce 已变化，请刷新页面后重试。', 'Account nonce changed. Refresh and retry.')
  return msg || ui('请求处理失败，请重试。', 'Request failed. Retry.')
}


                                                                                                                                                                                                                                
async function openSettings() {
  await loadSettingsState()
  page.value = 'settings'
}

async function saveSettings() {
  if (settingsSaving.value) return
  settingsSaving.value = true
  const m = Math.max(1, Math.min(1440, Math.floor(Number(autoLockMinutes.value)) || 30))
  autoLockMinutes.value = m
  rememberVisibleEnvConfig()
  try {
    await bg({
      type: 'SET_SETTINGS',
      walletEnv: walletEnv.value,
      envConfigs: exportEnvConfigs(),
      explorerUrl: explorerUrl.value,
      nodeAddr: nodeAddr.value,
      autoLockMinutes: m,
    })
    await loadMain()
    page.value = 'main'
  } finally {
    settingsSaving.value = false
  }
}

async function resetWallet() {
  if (!confirm(t.value.confirmReset)) return
  await bg({ type: 'RESET_WALLET' })
  accountDataSeq += 1
  address.value = ''
  accounts.value = []
  hasMnemonic.value = false
  resetAccountScopedData()
  loadingBalance.value = false
  txHistoryLoading.value = false
  page.value = 'setup'
}

                                                                                                                                                                                                                             
function availableTokens() {
  const keys = Object.keys(balances.value || {})
  if (!keys.length) return ['FB']
  return keys.sort((a, b) => {
    if (a === 'FB') return -1
    if (b === 'FB') return 1
    return a.localeCompare(b)
  })
}

function normalizeSendToken() {
  const tokens = availableTokens()
  if (!tokens.length) {
    sendToken.value = 'FB'
    return
  }
  if (!tokens.includes(sendToken.value)) {
    sendToken.value = tokens[0]
  }
}

async function loadLocalTxHistory() {
  const targetAddress = address.value
  const seq = accountDataSeq
  if (!targetAddress) {
    txHistory.value = []
    return
  }
  txHistoryLoading.value = true
  txHistoryError.value = ''
  try {
    const res = await bg({ type: 'GET_LOCAL_TX_HISTORY', address: targetAddress, limit: 200 })
    if (!isCurrentAccountDataLoad(seq, targetAddress)) return
    txHistory.value = res.items || []
  } catch (e: any) {
    if (isCurrentAccountDataLoad(seq, targetAddress)) txHistoryError.value = e.message || t.value.loadHistoryFailed
  } finally {
    if (isCurrentAccountDataLoad(seq, targetAddress)) txHistoryLoading.value = false
  }
}

async function loadSendNonce() {
  const targetAddress = address.value
  if (!targetAddress) return
  sendNonceLoading.value = true
  sendNonceError.value = ''
  try {
    const res = await bg({ type: 'GET_NONCE', address: targetAddress })
    if (page.value === 'send' && address.value === targetAddress) sendNonce.value = String(res.nonce ?? '')
  } catch (e: any) {
    if (page.value === 'send' && address.value === targetAddress) sendNonceError.value = e?.message || String(e)
  } finally {
    if (page.value === 'send' && address.value === targetAddress) sendNonceLoading.value = false
  }
}

const selectedTokenBalance = computed(() => {
  const token = sendToken.value || 'FB'
  if (token === 'FB') return fbBalance()
  return fmtAmt(balances.value[token]?.balance ?? '0', tokenDecimals(token))
})

function transactionBalanceError(tx: any, fee: string, status: any, available: Record<string, any>): string {
  if (!isValidRentStatus(status)) return ui('租金余额暂不可用，请刷新余额', 'Rent balance unavailable. Refresh balances.')
  if (!/^\d+$/.test(fee)) return ui('手续费必须是非负整数', 'Fee must be a non-negative integer.')
  let requiredFB = BigInt(fee)
  if (tx?.type === 'transaction' && /^\d+$/.test(String(tx.amount ?? ''))) {
    const amount = BigInt(tx.amount)
    if (tx.tokenAddress === 'FB') requiredFB += amount
    else if (amount > BigInt(normalizeRawAmount(available[tx.tokenAddress]?.balance))) return ui('代币可用余额不足', 'Insufficient available token balance.')
  }
  if (tx?.type === 'witness_challenge') requiredFB += BigInt(normalizeRawAmount(tx.stakeAmount))
  return requiredFB > BigInt(status.spendable_fb)
    ? ui('扣除租金后的可花费 FB 不足以支付金额和手续费', 'Spendable FB after rent is insufficient for the amount and fee.') : ''
}

const sendBalanceError = computed(() => {
  if (loadingBalance.value) return t.value.balanceChecking
  if (balanceError.value) return balanceError.value
  let amount = '0'
  try { amount = toRawAmt(sendAmount.value || '0', tokenDecimals(sendToken.value)) } catch { return '' }
  return transactionBalanceError({ type: 'transaction', tokenAddress: sendToken.value, amount }, sendFee.value || '1000', rentStatus.value, balances.value)
})

const sendAmountError = computed(() => {
  const decimals = tokenDecimals(sendToken.value)
  if (decimals === null) return t.value.errTokenPrecision
  if (!sendAmount.value.trim()) return ''
  try {
    toRawAmt(sendAmount.value, decimals)
    return ''
  } catch (error: any) {
    return error?.message === 'amount exceeds asset precision' ? t.value.errAmountPrecision(decimals) : t.value.errAmount
  }
})

const canSubmitSend = computed(() => {
  if (sendLoading.value || sendNonceLoading.value || sendReceipt.value) return false
  if (!sendToken.value || !sendTo.value.trim() || !sendAmount.value.trim() || sendAmountError.value || sendBalanceError.value) return false
  const nonce = sendNonce.value.trim()
  if (!/^\d+$/.test(nonce) || BigInt(nonce) > 0xffffffffffffffffn) return false
  try {
    return BigInt(toRawAmt(sendAmount.value, tokenDecimals(sendToken.value))) > 0n
  } catch {
    return false
  }
})

function resetSendDraft() {
  sendTo.value = ''
  sendAmount.value = ''
  sendError.value = ''
  sendStatus.value = ''
  sendTxId.value = ''
  sendReceipt.value = null
}

async function openSendPage() {
  resetSendDraft()
  normalizeSendToken()
  page.value = 'send'
  await Promise.all([loadSendNonce(), refreshBalances()])
}

async function startAnotherSend() {
  if (sendLoading.value) return
  resetSendDraft()
  await Promise.all([loadSendNonce(), refreshBalances()])
}

function returnToWalletAfterSend() {
  if (sendLoading.value) return
  page.value = 'main'
  void refreshBalances()
}

async function openHistoryPage() {
  page.value = 'history'
  await loadLocalTxHistory()
}

function formatHistoryTime(ts: number) {
  if (!ts) return '-'
  return new Date(ts).toLocaleString()
}

function copyText(text: string) {
  if (!text) return
  navigator.clipboard.writeText(text)
}

function shortAddr(addr: string) {
  if (!addr) return ''
  return addr.length > 14 ? `${addr.slice(0, 8)}…${addr.slice(-6)}` : addr
}

function copyAddr() {
  if (address.value) navigator.clipboard.writeText(address.value)
}

function copyTxId() {
  if (sendTxId.value) {
    navigator.clipboard.writeText(sendTxId.value)
    const old = sendStatus.value
    const copied = t.value.txIdCopied
    sendStatus.value = copied
    setTimeout(() => { if (sendStatus.value === copied) sendStatus.value = old }, 2000)
  }
}

const TOKEN_DECIMALS = 8

function tokenMetadata(token: string) {
  return isApprovalPage.value ? approveBalances.value[token] : balances.value[token]
}

function tokenDecimals(token: string): number | null {
  return resolveTokenDecimals(token, tokenMetadata(token))
}

function tokenLabel(token: string): string {
  return tokenDisplayLabel(token, tokenMetadata(token))
}

function tokenTitle(token: string): string {
  return tokenDisplayTitle(token, tokenMetadata(token))
}

function toRawAmt(display: string | number, decimals: number | null = TOKEN_DECIMALS): string {
  return toRawAmount(display, decimals)
}

function fmtAmt(raw: string | number | undefined | null, decimals: number | null = TOKEN_DECIMALS): string {
  return formatRawAmount(raw, decimals)
}

function fbBalance() {
  return isValidRentStatus(rentStatus.value) ? fmtAmt(rentStatus.value?.spendable_fb, tokenDecimals('FB')) : '—'
}

function rentSummaryRows(status: any) {
  if (!isValidRentStatus(status)) return []
  return [
    { label: ui('账面可用 FB', 'Recorded available FB'), value: `${fmtAmt(status.available_fb)} FB` },
    { label: ui('待结算租金', 'Pending rent'), value: `${fmtAmt(status.pending_rent_fb)} FB` },
    { label: ui('欠付租金', 'Unpaid rent'), value: `${fmtAmt(status.unpaid_rent_fb)} FB` },
    { label: ui('余额对应区块', 'Balance as of block'), value: String(status.as_of_height) },
  ]
}

function balanceValue() {
  if (balanceUpdatedAt.value) return fbBalance()
  return loadingBalance.value ? '…' : '—'
}

async function fitPrimaryBalance() {
  await nextTick()
  const main = balanceMainRef.value
  const value = balanceValueRef.value
  if (!main || !value) return

  const maxSize = 34
  const minSize = 14
  balanceFontSize.value = maxSize
  await nextTick()

  const unit = main.querySelector<HTMLElement>('.balance-unit')
  const availableWidth = Math.max(80, main.clientWidth - (unit?.offsetWidth || 0) - 10)
  const naturalWidth = value.scrollWidth
  if (naturalWidth > availableWidth) {
    balanceFontSize.value = Math.max(minSize, Math.floor(maxSize * availableWidth / naturalWidth))
  }
}

watch(
  [page, balanceUpdatedAt, () => rentStatus.value?.spendable_fb, lang],
  () => { void fitPrimaryBalance() },
  { flush: 'post' },
)

const balanceStatusText = computed(() => {
  const time = balanceUpdatedAt.value
    ? new Date(balanceUpdatedAt.value).toLocaleTimeString(lang.value === 'zh' ? 'zh-CN' : 'en-US', {
        hour: '2-digit', minute: '2-digit', second: '2-digit',
      })
    : ''
  if (balanceError.value) {
    const error = t.value.balanceLoadFailed(balanceError.value)
    return time ? `${error} · ${t.value.balanceCachedAt(time)}` : error
  }
  if (!time) return ''
  return balanceCached.value ? t.value.balanceCachedAt(time) : t.value.balanceUpdatedAt(time)
})

function otherBalances() {
  return Object.entries(balances.value).filter(([k]) => k !== 'FB')
}

function tokenIconUrl(token: string, balance: any): string {
  if (balance?.metadata_address !== token || balance?.address !== token) return ''
  const curated = top200IconForNative(balance.native_chain, balance.native_contract)
  if (curated) return curated
  const path = balance.icon_url
  if (typeof path !== 'string' || !path.startsWith('/api/token-icon?')) return ''
  try {
    const base = new URL(explorerUrl.value)
    const resolved = new URL(path, base)
    return resolved.origin === base.origin && resolved.pathname === '/api/token-icon' && resolved.searchParams.get('addr') === token ? resolved.href : ''
  } catch { return '' }
}

function hideFailedTokenIcon(event: Event) {
  if (event.target instanceof HTMLImageElement) event.target.style.display = 'none'
}

</script>

<template>
  <div :class="['wallet-shell', { 'approval-shell': isApprovalLayout }]">

                                                                                                                                                                  
    <div v-if="page === 'lock'" class="page auth-page">
      <button class="lang-toggle" @click="toggleLang" :title="lang === 'zh' ? 'English' : '中文'">{{ lang === 'zh' ? 'EN' : '中' }}</button>
      <div class="logo-area">
        <div class="brand-mark" aria-hidden="true">
          <svg viewBox="0 0 64 64" role="img">
            <g fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round" stroke-width="3.2">
              <g id="frostbit-snow-arm">
                <path d="M32 9v46" />
                <path d="m32 19-7-8m7 8 7-8M32 45l-7 8m7-8 7 8" />
              </g>
              <use href="#frostbit-snow-arm" transform="rotate(60 32 32)" />
              <use href="#frostbit-snow-arm" transform="rotate(120 32 32)" />
            </g>
          </svg>
        </div>
        <h1>FrostBit</h1>
        <p class="tagline">{{ ui('安全掌控你的链上资产', 'Your assets. Your control.') }}</p>
      </div>
      <div class="card auth-card">
        <label class="field-label">{{ t.password }}</label>
        <input class="field-input" type="password" v-model="unlockPassword"
          :placeholder="t.enterPwd" :disabled="unlockLoading" @keyup.enter="unlock" @input="unlockBtnText = t.unlock" autocomplete="current-password" />
        <button class="btn-primary mt-3" :class="{ 'btn-shake': unlockShake }" :disabled="unlockLoading" @click="unlock">
          <span v-if="unlockLoading" class="spinner" aria-hidden="true"></span>
          {{ unlockBtnText }}
        </button>
        <div class="divider">{{ t.orSep }}</div>
        <button class="btn-ghost" :disabled="unlockLoading" @click="page = 'setup'">{{ t.createOrImport }}</button>
      </div>
      <p class="security-note"><span aria-hidden="true">●</span> {{ ui('密钥仅加密保存在本机', 'Keys stay encrypted on this device') }}</p>
    </div>

                                                                                                                                                             
    <div v-else-if="page === 'setup'" class="page">
      <nav class="top-nav">
        <button class="btn-icon" @click="page = 'lock'">←</button>
        <span class="nav-title">{{ t.setupTitle }}</span>
        <button class="btn-icon" @click="toggleLang">{{ lang === 'zh' ? 'EN' : '中' }}</button>
      </nav>
      <div class="tab-bar">
        <button :class="['tab', setupTab === 'create' && 'tab-active']" @click="setupTab = 'create'">{{ t.tabCreate }}</button>
        <button :class="['tab', setupTab === 'import' && 'tab-active']" @click="setupTab = 'import'">{{ t.tabImport }}</button>
      </div>

                     
      <div v-if="setupTab === 'create'" class="card">
        <div :class="['mnemonic-box', generatedMnemonic && 'mnemonic-filled']">
          {{ generatedMnemonic || t.mnemonicEmpty }}
        </div>
        <button class="btn-secondary" :disabled="setupLoading" @click="generateMnemonic">{{ t.genMnemonic }}</button>
        <label class="field-label">{{ t.password }}</label>
        <input class="field-input" type="password" v-model="createPassword" :disabled="setupLoading" :placeholder="t.pwdPlaceholder" />
        <label class="field-label">{{ t.confirmPassword }}</label>
        <input class="field-input" type="password" v-model="createPassword2" :disabled="setupLoading" :placeholder="t.pwdConfirm" />
        <p v-if="setupError" class="msg-error">{{ setupError }}</p>
        <button class="btn-primary mt-2" :disabled="!generatedMnemonic || setupLoading" @click="createWallet">
          <span v-if="setupLoading" class="spinner" aria-hidden="true"></span>{{ setupLoading ? t.working : t.createWallet }}
        </button>
        <p class="hint">{{ t.mnemonicWarn }}</p>
      </div>

                     
      <div v-else class="card">
        <label class="field-label">{{ t.mnemonicLabel }}</label>
        <textarea class="field-input" rows="3" v-model="importMnemonic" :disabled="setupLoading" placeholder="word1 word2 word3 ..."></textarea>
        <label class="field-label">{{ t.password }}</label>
        <input class="field-input" type="password" v-model="importPassword" :disabled="setupLoading" :placeholder="t.pwdPlaceholder" />
        <p v-if="setupError" class="msg-error">{{ setupError }}</p>
        <button class="btn-primary mt-2" :disabled="setupLoading" @click="importWallet">
          <span v-if="setupLoading" class="spinner" aria-hidden="true"></span>{{ setupLoading ? t.working : t.importWallet }}
        </button>
        <div class="divider">{{ t.orSep }}</div>
        <label class="field-label">{{ t.batchImportLabel }}</label>
        <p class="hint">{{ t.batchImportHint }}</p>
        <textarea class="field-input" rows="4" v-model="importKeysText" :disabled="setupLoading" placeholder="# one 32-byte private key per line&#10;0x0123...&#10;89ab... # optional comment" style="font-family: monospace; font-size: 11px;"></textarea>
        <button class="btn-secondary mt-2" :disabled="setupLoading" @click="importAccountsFromText">
          <span v-if="setupLoading" class="spinner" aria-hidden="true"></span>{{ setupLoading ? t.working : t.batchImportBtn }}
        </button>
        <p v-if="importFileError" class="msg-error">{{ importFileError }}</p>
        <p v-if="importFileStatus" class="msg-success">{{ importFileStatus }}</p>
      </div>
    </div>

                                                                                                                                                                              
    <div v-else-if="page === 'main'" class="page">
      <nav class="top-nav">
        <div class="addr-dropdown">
          <div class="addr-row" @click="toggleAccountList" :title="t.switchAccount">
            <span class="account-avatar">{{ Math.max(1, accounts.indexOf(address) + 1) }}</span>
            <span class="account-meta">
              <strong>{{ ui('账户', 'Account') }} {{ Math.max(1, accounts.indexOf(address) + 1) }}</strong>
              <code class="addr-code">{{ shortAddr(address) }}</code>
            </span>
            <span class="chevron" :class="{ 'chevron-open': showAccountList }">⌄</span>
          </div>
          <div v-if="showAccountList" class="account-list">
            <div v-for="(addr, idx) in accounts" :key="addr"
              :class="['account-item', addr === address && 'account-item-active']"
              @click="selectAccount(idx)">
              <code>{{ shortAddr(addr) }}</code>
              <span v-if="addr === address" class="checkmark">✓</span>
            </div>
            <button class="account-list-manage" @click.stop="openAccountsPage">＋ {{ t.manageAccounts }}</button>
          </div>
        </div>
        <div class="nav-actions">
          <span :class="['env-pill', `env-pill-${walletEnv}`]">{{ envLabel(walletEnv) }}</span>
          <button class="btn-icon" @click="toggleLang" :title="lang === 'zh' ? 'English' : '中文'">{{ lang === 'zh' ? 'EN' : '中' }}</button>
          <button class="btn-icon" @click="copyAddr" :title="t.copyAddr" :aria-label="t.copyAddr">
            <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="8" y="8" width="11" height="11" rx="2"/><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2"/></svg>
          </button>
          <button class="btn-icon" @click="openSettings" :title="t.settings" :aria-label="t.settings">
            <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1-2.8 2.8-.1-.1a1.7 1.7 0 0 0-1.9-.3 1.7 1.7 0 0 0-1 1.6v.2h-4V21a1.7 1.7 0 0 0-1-1.6 1.7 1.7 0 0 0-1.9.3l-.1.1L4.2 17l.1-.1a1.7 1.7 0 0 0 .3-1.9A1.7 1.7 0 0 0 3 14H2.8v-4H3a1.7 1.7 0 0 0 1.6-1 1.7 1.7 0 0 0-.3-1.9L4.2 7 7 4.2l.1.1A1.7 1.7 0 0 0 9 4.6 1.7 1.7 0 0 0 10 3V2.8h4V3a1.7 1.7 0 0 0 1 1.6 1.7 1.7 0 0 0 1.9-.3l.1-.1L19.8 7l-.1.1a1.7 1.7 0 0 0-.3 1.9 1.7 1.7 0 0 0 1.6 1h.2v4H21a1.7 1.7 0 0 0-1.6 1Z"/></svg>
          </button>
          <button class="btn-icon" @click="lock" :title="t.lockTitle" :aria-label="t.lockTitle">
            <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="5" y="10" width="14" height="10" rx="2"/><path d="M8 10V7a4 4 0 0 1 8 0v3"/></svg>
          </button>
        </div>
      </nav>

      <div class="balance-card">
        <div class="balance-card-head">
          <span>{{ ui('可花费余额 · 已扣租金', 'Spendable balance · after rent') }}</span>
          <button class="balance-refresh" @click="refreshBalances()" :disabled="loadingBalance" :title="t.refreshBalance" :aria-label="t.refreshBalance">
            <svg :class="{ spinning: loadingBalance }" viewBox="0 0 24 24" aria-hidden="true"><path d="M20 11a8 8 0 1 0-2.3 5.7"/><path d="M20 4v7h-7"/></svg>
          </button>
        </div>
        <div ref="balanceMainRef" class="balance-main">
          <span ref="balanceValueRef" class="balance-val" :style="{ fontSize: `${balanceFontSize}px` }" :title="balanceValue()">{{ balanceValue() }}</span>
          <span class="balance-unit">FB</span>
        </div>
        <div v-if="balanceStatusText" :class="['balance-status', { 'balance-status-error': balanceError }]">
          {{ balanceStatusText }}
        </div>
        <div v-if="rentStatus" class="rent-summary" aria-label="Rent balance details">
          <div v-for="row in rentSummaryRows(rentStatus)" :key="row.label" class="rent-summary-row"><span>{{ row.label }}</span><strong>{{ row.value }}</strong></div>
        </div>
        <div class="balance-list">
          <div v-for="[token, bal] in otherBalances()" :key="token" class="balance-row">
            <span class="balance-token" :title="tokenTitle(token)"><img v-if="tokenIconUrl(token, bal)" :key="tokenIconUrl(token, bal)" class="balance-token-icon" :src="tokenIconUrl(token, bal)" alt="" @error="hideFailedTokenIcon" />{{ tokenLabel(token) }}</span>
            <span>{{ fmtAmt(bal.balance ?? '0', tokenDecimals(token)) }}</span>
          </div>
        </div>
      </div>

      <div class="action-row">
        <button class="action-btn" @click="openSendPage">
          <span class="action-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="m7 17 10-10M8 7h9v9"/></svg></span>
          <span>{{ t.send }}</span>
        </button>
        <button class="action-btn" @click="openHistoryPage">
          <span class="action-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></svg></span>
          <span>{{ t.history }}</span>
        </button>
        <button class="action-btn" @click="openAccountsPage">
          <span class="action-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="8" cy="15" r="4"/><path d="m11 12 7-7m-3 3 3 3m-6 0 2 2"/></svg></span>
          <span>{{ t.manageAccounts }}</span>
        </button>
      </div>
    </div>

                                                                                                                                                                        
    <div v-else-if="page === 'accounts'" class="page">
      <nav class="top-nav">
        <button class="btn-icon" @click="page = 'main'">←</button>
        <span class="nav-title">{{ t.accountsTitle }}</span>
        <button class="btn-icon" @click="toggleLang">{{ lang === 'zh' ? 'EN' : '中' }}</button>
      </nav>

      <div class="card">
        <label class="field-label">{{ t.accountListTitle }} ({{ accounts.length }})</label>
        <div class="managed-account-list">
          <button v-for="(addr, idx) in accounts" :key="addr"
            :class="['managed-account-row', addr === address && 'managed-account-row-active']"
            @click="selectAccount(idx)">
            <code>{{ shortAddr(addr) }}</code>
            <span>{{ addr === address ? `✓ ${t.currentAccount}` : '' }}</span>
          </button>
        </div>
      </div>

      <div v-if="hasMnemonic" class="card">
        <strong>{{ t.deriveAccountTitle }}</strong>
        <p class="hint">{{ t.deriveAccountHint }}</p>
        <button class="btn-primary" :disabled="accountActionLoading" @click="addDerivedAccount">
          {{ accountActionLoading ? t.working : t.addDerivedAccount }}
        </button>
      </div>

      <div class="card">
        <strong>{{ t.privateKeyTitle }}</strong>
        <label class="field-label">{{ t.privateKeyLabel }}</label>
        <p class="hint">{{ t.privateKeyHint }}</p>
        <textarea class="field-input key-input" rows="4" v-model="accountPrivateKeysText"
          placeholder="0x0123...&#10;89ab... # optional comment"></textarea>
        <button class="btn-secondary" :disabled="accountActionLoading" @click="importPrivateKeysUnlocked">
          {{ accountActionLoading ? t.working : t.importPrivateKeys }}
        </button>
        <p v-if="accountActionError" class="msg-error">{{ accountActionError }}</p>
        <p v-if="accountActionStatus" class="msg-success">{{ accountActionStatus }}</p>
      </div>
    </div>

                                                                                                                                                                              
    <div v-else-if="page === 'send'" class="page">
      <nav class="top-nav">
        <button class="btn-icon nav-back" :disabled="sendLoading" @click="page = 'main'" :aria-label="t.backWallet">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m15 18-6-6 6-6"/></svg>
        </button>
        <span class="nav-title">{{ t.sendTokenTitle }}</span>
        <span class="nav-spacer"></span>
      </nav>

      <div v-if="!sendReceipt" class="card send-card">
        <div class="send-balance-strip">
          <span>{{ t.availableBalance }}</span>
          <strong :title="tokenTitle(sendToken)">{{ selectedTokenBalance }} {{ tokenLabel(sendToken) }}</strong>
        </div>
        <div v-if="rentStatus" class="rent-summary" aria-label="Rent balance details">
          <div class="rent-summary-row"><span>{{ ui('可花费 FB', 'Spendable FB') }}</span><strong>{{ fbBalance() }} FB</strong></div>
          <div v-for="row in rentSummaryRows(rentStatus)" :key="row.label" class="rent-summary-row"><span>{{ row.label }}</span><strong>{{ row.value }}</strong></div>
        </div>

        <div class="field-group">
          <label class="field-label" for="send-token">{{ t.token }}</label>
          <select id="send-token" class="field-input" v-model="sendToken" :disabled="sendLoading" :title="tokenTitle(sendToken)">
            <option v-for="token in availableTokens()" :key="token" :value="token" :title="tokenTitle(token)">{{ tokenLabel(token) }}</option>
          </select>
        </div>
        <div class="field-group">
          <label class="field-label">{{ t.recvAddr }}</label>
          <input class="field-input mono-input" type="text" v-model="sendTo" :disabled="sendLoading" placeholder="fbc1q..." autocomplete="off" spellcheck="false" />
        </div>
        <div class="field-group">
          <label class="field-label">{{ t.amount }}</label>
          <div class="amount-input-wrap">
            <input class="field-input amount-input" type="text" inputmode="decimal" v-model="sendAmount" :disabled="sendLoading" placeholder="0.00" />
            <span :title="tokenTitle(sendToken)">{{ tokenLabel(sendToken) }}</span>
          </div>
        </div>
        <div class="field-grid">
          <div class="field-group">
            <label class="field-label">Fee <span>{{ t.feeUnit }}</span></label>
            <input class="field-input mono-input" type="text" inputmode="numeric" v-model="sendFee" :disabled="sendLoading" placeholder="1000" />
          </div>
          <div class="field-group">
            <label class="field-label">{{ t.nonce }}</label>
            <div class="nonce-input-wrap">
              <input class="field-input mono-input" type="text" inputmode="numeric" v-model="sendNonce"
                :disabled="sendNonceLoading || sendLoading" :placeholder="sendNonceLoading ? t.nonceLoading : '0'" />
              <button class="input-action" type="button" :disabled="sendNonceLoading || sendLoading"
                :title="t.refreshNonce" :aria-label="t.refreshNonce" @click="loadSendNonce">
                <svg :class="{ spinning: sendNonceLoading }" viewBox="0 0 24 24" aria-hidden="true"><path d="M20 11a8 8 0 1 0-2.3 5.7"/><path d="M20 4v7h-7"/></svg>
              </button>
            </div>
          </div>
        </div>
        <p class="field-help">{{ t.nonceHint }}</p>
        <div v-if="sendNonceError" class="status-banner status-error">{{ sendNonceError }}</div>
        <div v-if="sendRentActivationHint" class="status-banner status-warn">{{ sendRentActivationHint }}</div>
        <div v-if="sendAmountError" class="status-banner status-warn">{{ sendAmountError }}</div>
        <div v-if="sendBalanceError && !sendAmountError" class="status-banner status-warn">{{ sendBalanceError }}</div>
        <div v-if="sendError && sendError !== sendAmountError" class="status-banner status-error">{{ sendError }}</div>

        <button class="btn-primary send-submit" @click="confirmSend" :disabled="!canSubmitSend">
          <span v-if="sendLoading" class="spinner" aria-hidden="true"></span>
          <svg v-else viewBox="0 0 24 24" aria-hidden="true"><path d="m7 17 10-10M8 7h9v9"/></svg>
          {{ sendLoading ? t.sending : t.confirmSend }}
        </button>
      </div>

      <div v-else class="card send-success-card" aria-live="polite">
        <div class="success-orb" aria-hidden="true">
          <svg viewBox="0 0 24 24"><path d="m6.5 12.5 3.5 3.5 7.5-8"/></svg>
        </div>
        <div class="success-copy">
          <span class="success-kicker">{{ t.submittedTx }}</span>
          <h2>{{ t.sentTitle }}</h2>
          <p>{{ t.sentSubtitle }}</p>
        </div>
        <div class="receipt-summary">
          <div><span>{{ t.amount }}</span><strong :title="tokenTitle(sendReceipt.token)">{{ sendReceipt.amount }} {{ tokenLabel(sendReceipt.token) }}</strong></div>
          <div><span>{{ t.recvAddr }}</span><code :title="sendReceipt.to">{{ shortAddr(sendReceipt.to) }}</code></div>
          <div><span>{{ t.nonce }}</span><code>{{ sendReceipt.nonce }}</code></div>
          <div><span>TxID</span><code :title="sendReceipt.txId">{{ shortAddr(sendReceipt.txId) || '-' }}</code></div>
        </div>
        <div v-if="sendStatus" class="status-banner status-success">{{ sendStatus }}</div>
        <button v-if="sendReceipt.txId" class="btn-secondary" :disabled="sendLoading" @click="copyTxId">{{ t.copyFullTxId }}</button>
        <div class="success-actions">
          <button class="btn-primary" :disabled="sendLoading" @click="returnToWalletAfterSend">{{ t.backWallet }}</button>
          <button class="btn-ghost" :disabled="sendLoading" @click="startAnotherSend">{{ t.sendAnother }}</button>
        </div>
      </div>
    </div>

                                                                                                                                                                              
    <div v-else-if="page === 'history'" class="page">
      <nav class="top-nav">
        <button class="btn-icon" @click="page = 'main'">⬅</button>
        <span class="nav-title">{{ t.localHistory }}</span>
        <button class="btn-icon" @click="loadLocalTxHistory" :disabled="txHistoryLoading" :title="t.refresh">↻</button>
      </nav>
      <div class="card">
        <p class="hint">{{ t.historyHint }}</p>
        <p class="hint mono-mini">{{ shortAddr(address) }}</p>
        <p v-if="txHistoryError" class="msg-error">{{ txHistoryError }}</p>
        <div v-if="txHistoryLoading" class="hint">{{ t.loading }}</div>
        <div v-else-if="txHistory.length === 0" class="hint">{{ t.noHistory }}</div>
        <div v-else class="tx-history-list">
          <div v-for="item in txHistory" :key="item.localId" class="tx-history-item">
            <div class="tx-history-row">
              <span class="balance-token" :title="tokenTitle(item.tokenAddress || '')">{{ tokenLabel(item.tokenAddress || '') }}</span>
              <span>{{ item.amount ? fmtAmt(item.amount, tokenDecimals(item.tokenAddress || '')) : '-' }}</span>
            </div>
            <div class="tx-history-row mono-mini">
              <span>{{ t.to }} {{ shortAddr(item.toAddress || '-') }}</span>
              <span>{{ formatHistoryTime(item.createdAt) }}</span>
            </div>
            <div class="tx-history-row mono-mini">
              <span>TxID: {{ item.txId ? shortAddr(item.txId) : '-' }}</span>
              <button v-if="item.txId" class="btn-ghost tx-copy-btn" @click="copyText(item.txId)">{{ t.copy }}</button>
            </div>
          </div>
        </div>
      </div>
    </div>
    <div v-else-if="page === 'settings'" class="page">
      <nav class="top-nav">
        <button class="btn-icon" @click="page = 'main'">←</button>
        <span class="nav-title">{{ t.settings }}</span>
        <button class="btn-icon" @click="toggleLang">{{ lang === 'zh' ? 'EN' : '中' }}</button>
      </nav>
      <div class="card">
        <label class="field-label">{{ ui('运行环境', 'Environment') }}</label>
        <div class="env-switch">
          <button
            v-for="profile in WALLET_ENV_PROFILES"
            :key="profile.key"
            type="button"
            :class="['env-option', walletEnv === profile.key && 'env-option-active']"
            @click="selectWalletEnv(profile.key)">
            {{ envLabel(profile.key) }}
          </button>
        </div>
        <p class="hint">{{ ui('每个环境独立保存 Explorer 和节点地址，保存后生效。', 'Explorer and node settings are saved separately per environment. Save to apply.') }}</p>
        <label class="field-label">{{ t.explorerAddr }}</label>
        <input class="field-input" type="text" v-model="explorerUrl" :placeholder="explorerPlaceholder" />
        <label class="field-label">{{ t.nodeAddr }}</label>
        <input class="field-input" type="text" v-model="nodeAddr" :placeholder="nodePlaceholder" />
        <label class="field-label">{{ t.autoLock }}</label>
        <input class="field-input" type="number" min="1" max="1440" v-model.number="autoLockMinutes" />
        <p class="hint">{{ t.autoLockHint }}</p>
        <button class="btn-primary mt-2" :disabled="settingsSaving" @click="saveSettings">
          <span v-if="settingsSaving" class="spinner" aria-hidden="true"></span>{{ settingsSaving ? t.saving : t.save }}
        </button>
      </div>
      <div class="card danger-card">
        <p class="danger-hint">{{ t.dangerOp }}</p>
        <button class="btn-danger" @click="resetWallet">{{ t.resetWallet }}</button>
      </div>
    </div>

                                                                                                                                   
    <div v-else-if="page === 'approve-fee'" class="page">
      <nav class="top-nav">
        <span class="nav-title">{{ t.approveFeeTitle }}</span>
        <button class="btn-icon" @click="toggleLang">{{ lang === 'zh' ? 'EN' : '中' }}</button>
      </nav>
      <div v-if="pendingRequests.length > 0" class="card">
        <p class="hint">{{ t.source }} {{ pendingRequests[0].url }}</p>
        <div class="approval-primary">
          <div class="approval-title">{{ txTypeTitle(pendingRequests[0].txDesc.type) }}</div>
          <div class="approval-subtitle">{{ pendingRequests[0].url }}</div>
        </div>
        <div class="approval-summary compact">
          <div v-for="row in approvalSummaryRows.slice(0, 5)" :key="row.label" class="approval-row">
            <span>{{ row.label }}</span>
            <b :title="row.title">{{ row.value }}</b>
          </div>
        </div>

        <label class="field-label" style="margin-top:8px">{{ t.signAccount }}</label>
        <select class="field-input" v-model.number="approveAccountIdx">
          <option v-for="(addr, idx) in accounts" :key="addr" :value="idx">{{ addr === address ? '● ' : '' }}{{ shortAddr(addr) }}</option>
        </select>

        <label class="field-label" style="margin-top:8px">Fee <span style="color:#8b949e;font-size:10px;font-weight:400">{{ t.feeDefault }}</span></label>
        <input class="field-input" type="text" v-model="approveFee" placeholder="1000" />
        <div class="rent-summary" aria-label="Rent balance details">
          <div class="rent-summary-row"><span>{{ ui('可花费 FB', 'Spendable FB') }}</span><strong>{{ approveSignerFbDisplay }} FB</strong></div>
          <div v-for="row in rentSummaryRows(approveRentStatus)" :key="row.label" class="rent-summary-row"><span>{{ row.label }}</span><strong>{{ row.value }}</strong></div>
        </div>
        <p v-if="approveBalanceCheckError" class="msg-error">{{ approveBalanceCheckError }}</p>

        <div v-if="isWitnessChallengeRequest" class="tx-summary">
          <div class="tx-summary-row">
            <span>{{ t.challengeStakeLbl }}</span>
            <b>{{ challengeStakeDisplay }} FB</b>
          </div>
          <div class="tx-summary-row">
            <span>{{ t.challengeTotalLbl }}</span>
            <b>{{ challengeTotalDisplay }} FB</b>
          </div>
        </div>
        <p v-if="approveError" class="msg-error">{{ approveError }}</p>

        <div style="display: flex; gap: 10px; margin-top: 15px;">
          <button class="btn-ghost" style="flex:1" :disabled="!!approvalAction" @click="rejectRequest(pendingRequests[0].id)">
            <span v-if="approvalAction === 'reject'" class="spinner" aria-hidden="true"></span>{{ approvalAction === 'reject' ? t.rejecting : t.reject }}
          </button>
          <button class="btn-primary" style="flex:1" :disabled="!!approvalAction || !!approveBalanceCheckError" @click="confirmFeeAndContinue">{{ t.next }}</button>
        </div>
      </div>
    </div>

                                                                                                                                     
    <div v-else-if="page === 'approve'" class="page">
      <nav class="top-nav">
        <button v-if="isTxType(pendingRequests[0]?.txDesc?.type)" class="btn-icon" @click="page = 'approve-fee'">←</button>
        <span class="nav-title">{{ t.approveTitle }}</span>
        <button class="btn-icon" @click="toggleLang">{{ lang === 'zh' ? 'EN' : '中' }}</button>
      </nav>
      <div v-if="pendingRequests.length > 0" class="card">
        <p class="hint">{{ t.source }} {{ pendingRequests[0].url }}</p>
        <div style="display: flex; flex-direction: column; gap: 8px;">
          <div class="approval-primary">
            <div class="approval-title">{{ txTypeTitle(pendingRequests[0].txDesc.type) }}</div>
            <div class="approval-subtitle">{{ approvalSubtitle }}</div>
          </div>

          <div v-if="isTxType(pendingRequests[0].txDesc.type)" class="fee-badge">
            {{ t.signAccountLbl }} {{ shortAddr(accounts[approveAccountIdx] || address) }} ｜ {{ t.feeLbl }} {{ feeRawDisplay(approveFee) }}
          </div>
          <div v-if="isTxType(pendingRequests[0].txDesc.type)" class="rent-summary" aria-label="Rent balance details">
            <div class="rent-summary-row"><span>{{ ui('可花费 FB', 'Spendable FB') }}</span><strong>{{ approveSignerFbDisplay }} FB</strong></div>
            <div v-for="row in rentSummaryRows(approveRentStatus)" :key="row.label" class="rent-summary-row"><span>{{ row.label }}</span><strong>{{ row.value }}</strong></div>
          </div>
          <p v-if="approveBalanceCheckError" class="msg-error">{{ approveBalanceCheckError }}</p>

          <div class="approval-summary">
            <div v-for="row in approvalSummaryRows" :key="row.label" class="approval-row">
              <span>{{ row.label }}</span>
              <b :title="row.title">{{ row.value }}</b>
            </div>
          </div>

          <div v-if="isWitnessChallengeRequest" class="tx-summary">
            <div class="tx-summary-row">
              <span>{{ t.challengeStakeLbl }}</span>
              <b>{{ challengeStakeDisplay }} FB</b>
            </div>
            <div class="tx-summary-row">
              <span>{{ t.feeLbl }}</span>
              <b>{{ challengeFeeDisplay }} FB</b>
            </div>
            <div class="tx-summary-row">
              <span>{{ t.challengeTotalLbl }}</span>
              <b>{{ challengeTotalDisplay }} FB</b>
            </div>
            <div class="tx-summary-row">
              <span>{{ t.fbBalanceLbl }}</span>
              <b v-if="loadingApproveBalance">{{ t.balanceChecking }}</b>
              <b v-else>{{ approveSignerFbDisplay }} FB</b>
            </div>
            <div class="tx-summary-hint" :class="challengeBalanceEnough ? 'ok' : 'warn'">
              {{ loadingApproveBalance ? t.balanceChecking : (challengeBalanceEnough ? t.challengeBalanceOk : t.challengeBalanceLow(challengeTotalDisplay)) }}
            </div>
            <div v-if="approveBalanceError" class="tx-summary-hint warn">{{ approveBalanceError }}</div>
          </div>

          <label class="field-label">{{ t.opDetail }}</label>
          <pre class="field-input" style="white-space: pre-wrap; word-break: break-all; font-size: 11px; max-height: 200px; overflow-y: auto;">{{ JSON.stringify(pendingRequests[0].txDesc, null, 2) }}</pre>
          <p v-if="approveError" class="msg-error">{{ approveError }}</p>
        </div>

        <div style="display: flex; gap: 10px; margin-top: 15px;">
          <button class="btn-ghost" style="flex:1" :disabled="!!approvalAction" @click="rejectRequest(pendingRequests[0].id)">
            <span v-if="approvalAction === 'reject'" class="spinner" aria-hidden="true"></span>{{ approvalAction === 'reject' ? t.rejecting : t.reject }}
          </button>
          <button class="btn-primary" style="flex:1" :disabled="!!approvalAction || !!approveBalanceCheckError" @click="approveRequest(pendingRequests[0].id)">
            <span v-if="approvalAction === 'approve'" class="spinner" aria-hidden="true"></span>{{ approvalAction === 'approve' ? t.approving : t.approve }}
          </button>
        </div>
      </div>
    </div>

  </div>
</template>

<style scoped>
.wallet-shell {
  font-family: 'Outfit', system-ui, sans-serif;
  width: 360px;
  min-height: 480px;
  background: #0d1117;
  color: #e6edf3;
}

.page {
  display: flex;
  flex-direction: column;
  min-height: 480px;
  padding: 0 0 16px;
}

                                                                                                                                                               
.logo-area {
  text-align: center;
  padding: 40px 16px 20px;
}

.logo-snowflake {
  font-size: 56px;
  line-height: 1;
  margin-bottom: 8px;
  background: linear-gradient(135deg, #58a6ff, #a5d6ff);
  -webkit-background-clip: text;
  background-clip: text;
  -webkit-text-fill-color: transparent;
}

h1 {
  font-size: 26px;
  font-weight: 800;
  background: linear-gradient(135deg, #58a6ff, #a5d6ff);
  -webkit-background-clip: text;
  background-clip: text;
  -webkit-text-fill-color: transparent;
  margin-bottom: 4px;
}

.tagline { color: #8b949e; font-size: 12px; }

                                                                                                                                                               
.card {
  margin: 0 12px 12px;
  background: #161b22;
  border: 1px solid #30363d;
  border-radius: 12px;
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

                                                                                                                                                                 
.top-nav {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 12px 10px;
  border-bottom: 1px solid #21262d;
  margin-bottom: 12px;
}

.nav-title { font-size: 15px; font-weight: 600; }
.nav-actions { display: flex; gap: 4px; }
.env-pill {
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0;
  padding: 3px 8px;
  border-radius: 999px;
  border: 1px solid #30363d;
  background: #21262d;
  color: #8b949e;
}
.env-pill-test { border-color: rgba(227, 179, 65, 0.45); color: #e3b341; }
.env-pill-prod { border-color: rgba(63, 185, 80, 0.45); color: #3fb950; }

.addr-row {
  display: flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
  padding: 4px 8px;
  border-radius: 8px;
  transition: background 0.15s;
}

.addr-row:hover { background: #21262d; }
.addr-code { font-family: 'JetBrains Mono', monospace; font-size: 12px; color: #8b949e; }
.copy-icon { font-size: 14px; color: #8b949e; }

                                                                                                                                                         
.btn-primary {
  width: 100%;
  background: linear-gradient(135deg, #1f6feb, #58a6ff);
  color: #fff;
  border: none;
  border-radius: 8px;
  padding: 10px 16px;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition: opacity 0.15s;
  font-family: inherit;
}
.btn-primary:hover:not(:disabled) { opacity: 0.85; }
.btn-primary:disabled { opacity: 0.4; cursor: not-allowed; }
.lang-toggle {
  position: absolute; top: 10px; right: 12px;
  background: #21262d; color: #8b949e; border: 1px solid #30363d;
  border-radius: 6px; padding: 4px 10px; font-size: 11px; cursor: pointer;
  font-family: inherit; transition: color 0.15s, border-color 0.15s;
}
.lang-toggle:hover { color: #58a6ff; border-color: #58a6ff; }
.btn-shake { animation: shake 0.45s cubic-bezier(.36,.07,.19,.97) both; background: linear-gradient(135deg, #da3633, #f85149); }
@keyframes shake {
  10%, 90% { transform: translateX(-2px); }
  20%, 80% { transform: translateX(4px); }
  30%, 50%, 70% { transform: translateX(-6px); }
  40%, 60% { transform: translateX(6px); }
}

.btn-secondary {
  background: #21262d;
  color: #58a6ff;
  border: 1px solid #30363d;
  border-radius: 8px;
  padding: 8px 14px;
  font-size: 13px;
  cursor: pointer;
  transition: border-color 0.15s;
  font-family: inherit;
}
.btn-secondary:hover { border-color: #58a6ff; }

.btn-ghost {
  background: none;
  border: 1px solid #30363d;
  color: #8b949e;
  border-radius: 8px;
  padding: 8px 14px;
  font-size: 13px;
  cursor: pointer;
  transition: all 0.15s;
  font-family: inherit;
}
.btn-ghost:hover { border-color: #58a6ff; color: #58a6ff; }

.btn-icon {
  background: none;
  border: none;
  color: #8b949e;
  font-size: 17px;
  padding: 4px 8px;
  border-radius: 6px;
  cursor: pointer;
  transition: color 0.15s;
  font-family: inherit;
}
.btn-icon:hover { color: #e6edf3; }

.btn-danger {
  background: rgba(248, 81, 73, 0.1);
  border: 1px solid #f85149;
  color: #f85149;
  border-radius: 8px;
  padding: 10px 16px;
  font-size: 14px;
  cursor: pointer;
  font-family: inherit;
  width: 100%;
}
.btn-danger:hover { background: rgba(248, 81, 73, 0.2); }

.btn-refresh {
  background: #21262d;
  border: 1px solid #30363d;
  color: #8b949e;
  border-radius: 6px;
  padding: 5px 10px;
  font-size: 12px;
  cursor: pointer;
  transition: all 0.15s;
  font-family: inherit;
  align-self: flex-start;
}
.btn-refresh:hover { border-color: #58a6ff; color: #58a6ff; }
.btn-refresh:disabled { opacity: 0.5; cursor: not-allowed; }

                                                                                                                                                               
.field-label { font-size: 12px; color: #8b949e; font-weight: 500; margin-bottom: -4px; }

.field-input {
  background: #21262d;
  border: 1px solid #30363d;
  border-radius: 8px;
  padding: 9px 12px;
  color: #e6edf3;
  font-size: 13px;
  outline: none;
  transition: border-color 0.15s;
  font-family: inherit;
  width: 100%;
}
.field-input:focus { border-color: #58a6ff; }
textarea.field-input { resize: none; line-height: 1.5; }
.env-switch {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 4px;
  padding: 3px;
  border: 1px solid #30363d;
  border-radius: 8px;
  background: #0d1117;
}
.env-option {
  border: 0;
  border-radius: 6px;
  padding: 7px 6px;
  background: transparent;
  color: #8b949e;
  cursor: pointer;
  font-size: 12px;
  font-weight: 600;
  font-family: inherit;
}
.env-option-active {
  background: #21262d;
  color: #e6edf3;
}

                                                                                                                                                               
.tab-bar {
  display: flex;
  margin: 0 12px 8px;
  background: #161b22;
  border: 1px solid #30363d;
  border-radius: 8px;
  padding: 3px;
  gap: 2px;
}

.tab {
  flex: 1;
  background: none;
  border: none;
  color: #8b949e;
  padding: 7px;
  border-radius: 6px;
  cursor: pointer;
  font-size: 13px;
  font-weight: 500;
  font-family: inherit;
  transition: all 0.15s;
}

.tab-active {
  background: #21262d;
  color: #e6edf3;
}

                                                                                                                                                       
.mnemonic-box {
  background: #21262d;
  border: 1px dashed #30363d;
  border-radius: 8px;
  padding: 12px;
  font-family: 'JetBrains Mono', monospace;
  font-size: 13px;
  line-height: 1.8;
  color: #8b949e;
  text-align: center;
  min-height: 64px;
  word-break: break-word;
}

.mnemonic-filled { color: #58a6ff; border-color: #58a6ff; }

                                                                                                                                                         
.balance-card {
  margin: 0 12px 12px;
  background: linear-gradient(135deg, #0c1e35, #0d1a2d);
  border: 1px solid #1f4068;
  border-radius: 12px;
  padding: 20px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.balance-main { display: flex; align-items: baseline; gap: 8px; }
.balance-val { font-size: 36px; font-weight: 800; }
.balance-unit { font-size: 15px; color: #8b949e; }
.balance-status { color: #8b949e; font-size: 11px; line-height: 1.4; overflow-wrap: anywhere; }
.balance-status-error { color: #f0883e; }

.balance-list { display: flex; flex-direction: column; gap: 4px; }
.balance-row {
  display: flex;
  justify-content: space-between;
  font-size: 13px;
  padding: 4px 0;
  border-bottom: 1px solid rgba(255, 255, 255, 0.04);
  color: #8b949e;
}
.balance-row:last-child { border-bottom: none; }
.balance-token {
  max-width: 170px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 500;
  color: #e6edf3;
}
.balance-row span:last-child { flex-shrink: 0; margin-left: 12px; }

                                                                                                                                                         
.action-row { display: flex; gap: 10px; padding: 0 12px; }

.action-btn {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  flex: 1;
  background: #161b22;
  border: 1px solid #30363d;
  border-radius: 12px;
  padding: 14px 10px;
  color: #e6edf3;
  cursor: pointer;
  font-size: 13px;
  font-weight: 500;
  font-family: inherit;
  transition: border-color 0.15s;
}
.action-btn:hover { border-color: #58a6ff; }
.action-icon { font-size: 20px; }

.tx-history-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.tx-history-item {
  border: 1px solid #30363d;
  border-radius: 10px;
  padding: 10px;
  background: #11161d;
}

.tx-history-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.mono-mini {
  font-family: 'JetBrains Mono', monospace;
  font-size: 11px;
  color: #8b949e;
}

.tx-copy-btn {
  padding: 2px 8px;
  font-size: 11px;
  min-width: auto;
}

                                                                                                                                                       
.msg-error { color: #f85149; font-size: 12px; }
.msg-success { color: #3fb950; font-size: 12px; }
.hint { color: #e3b341; font-size: 11px; }
.rent-activation-hint {
  color: #e3b341;
  background: rgba(227, 179, 65, 0.08);
  border: 1px solid rgba(227, 179, 65, 0.28);
  border-radius: 8px;
  padding: 8px 10px;
  font-size: 11px;
  line-height: 1.4;
}

                                                                                                                                                           
.danger-card { border-color: rgba(248, 81, 73, 0.3); }
.danger-hint { color: #8b949e; font-size: 12px; }

                                                                                                                                                             
.divider {
  text-align: center;
  color: #8b949e;
  font-size: 12px;
  margin: 2px 0;
}
.mt-2 { margin-top: 4px; }
.mt-3 { margin-top: 6px; }

                                                                                                                                                
.addr-dropdown { position: relative; }

.account-list {
  position: absolute;
  top: calc(100% + 4px);
  left: 0;
  background: #1c2128;
  border: 1px solid #30363d;
  border-radius: 8px;
  min-width: 200px;
  z-index: 100;
  box-shadow: 0 8px 24px rgba(0,0,0,0.5);
  overflow: hidden;
}

.account-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 9px 12px;
  cursor: pointer;
  font-size: 12px;
  color: #8b949e;
  font-family: 'JetBrains Mono', monospace;
  transition: background 0.12s;
}
.account-item:hover { background: #21262d; color: #e6edf3; }
.account-item-active { color: #58a6ff; }
.checkmark { color: #3fb950; font-weight: 700; }
.account-list-manage {
  width: 100%;
  border: 0;
  border-top: 1px solid #30363d;
  background: #161b22;
  color: #58a6ff;
  padding: 9px 12px;
  text-align: left;
  font: inherit;
  font-size: 12px;
  cursor: pointer;
}
.account-list-manage:hover { background: #21262d; }

.managed-account-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.managed-account-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  width: 100%;
  padding: 9px 10px;
  border: 1px solid #30363d;
  border-radius: 8px;
  background: #0d1117;
  color: #8b949e;
  cursor: pointer;
  font: inherit;
  font-size: 11px;
}
.managed-account-row code { color: inherit; font-family: 'JetBrains Mono', monospace; }
.managed-account-row:hover { border-color: #58a6ff; color: #e6edf3; }
.managed-account-row-active { border-color: rgba(88, 166, 255, 0.55); color: #58a6ff; }
.key-input { font-family: 'JetBrains Mono', monospace; font-size: 11px; }

                                                                                                                                                      
.file-input {
  background: #21262d;
  border: 1px dashed #30363d;
  border-radius: 8px;
  padding: 9px 12px;
  color: #8b949e;
  font-size: 12px;
  cursor: pointer;
  width: 100%;
  transition: border-color 0.15s;
}
.file-input:hover { border-color: #58a6ff; }

.fee-badge {
  background: rgba(88, 166, 255, 0.1);
  border: 1px solid rgba(88, 166, 255, 0.3);
  border-radius: 8px;
  padding: 8px 12px;
  font-size: 13px;
  font-weight: 600;
  color: #58a6ff;
  font-family: 'JetBrains Mono', monospace;
}
.approval-primary {
  padding: 12px;
  border-radius: 10px;
  background: linear-gradient(135deg, rgba(88,166,255,0.14), rgba(63,185,80,0.08));
  border: 1px solid rgba(88,166,255,0.22);
}
.approval-title {
  font-size: 17px;
  font-weight: 800;
  color: #f0f6fc;
  margin-bottom: 4px;
}
.approval-subtitle {
  font-size: 12px;
  line-height: 1.45;
  color: #8b949e;
  overflow-wrap: anywhere;
}
.approval-summary {
  display: flex;
  flex-direction: column;
  gap: 7px;
  padding: 10px 12px;
  border-radius: 10px;
  background: #0d1117;
  border: 1px solid #30363d;
}
.approval-summary.compact { margin-top: 8px; }
.approval-row {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 12px;
  font-size: 12px;
}
.approval-row span {
  color: #8b949e;
  flex: 0 0 auto;
}
.approval-row b {
  color: #f0f6fc;
  font-family: 'JetBrains Mono', monospace;
  font-size: 11px;
  font-weight: 600;
  text-align: right;
  overflow-wrap: anywhere;
}
.tx-summary {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 10px 12px;
  border-radius: 10px;
  background: rgba(227, 179, 65, 0.08);
  border: 1px solid rgba(227, 179, 65, 0.18);
}
.tx-summary-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  font-size: 12px;
  color: #c9d1d9;
}
.tx-summary-row b {
  color: #f0f6fc;
  font-family: 'JetBrains Mono', monospace;
}
.tx-summary-hint {
  font-size: 11px;
  line-height: 1.5;
  padding: 8px 10px;
  border-radius: 8px;
}
.tx-summary-hint.ok {
  color: #3fb950;
  background: rgba(63, 185, 80, 0.1);
}
.tx-summary-hint.warn {
  color: #e3b341;
  background: rgba(227, 179, 65, 0.12);
}

                                                                                                                           
.wallet-shell {
  --bg: #07101f;
  --surface: rgba(15, 27, 46, 0.9);
  --surface-strong: #111f34;
  --surface-soft: rgba(20, 36, 59, 0.72);
  --border: rgba(148, 163, 184, 0.14);
  --border-strong: rgba(111, 211, 255, 0.34);
  --text: #f4f8ff;
  --muted: #8fa2bb;
  --muted-strong: #b7c5d8;
  --accent: #61d7ff;
  --accent-blue: #4f86ff;
  --success: #55e6a5;
  --warning: #ffd166;
  --danger: #ff7184;
  width: 380px;
  min-height: 540px;
  overflow: hidden;
  color: var(--text);
  background:
    radial-gradient(circle at 12% -10%, rgba(64, 166, 255, 0.2), transparent 38%),
    radial-gradient(circle at 96% 110%, rgba(46, 213, 188, 0.12), transparent 35%),
    var(--bg);
  font-family: Inter, ui-sans-serif, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  letter-spacing: -0.01em;
}

.page {
  position: relative;
  min-height: 540px;
  padding-bottom: 22px;
}

.auth-page::before {
  content: '';
  position: absolute;
  width: 230px;
  height: 230px;
  left: 75px;
  top: -105px;
  border-radius: 50%;
  background: rgba(70, 176, 255, 0.16);
  filter: blur(34px);
  pointer-events: none;
}

.logo-area {
  position: relative;
  padding: 56px 20px 28px;
}

.brand-mark {
  display: grid;
  place-items: center;
  width: 70px;
  height: 70px;
  margin: 0 auto 16px;
  border: 1px solid rgba(139, 222, 255, 0.28);
  border-radius: 22px;
  color: #d9f6ff;
  background: linear-gradient(145deg, rgba(69, 177, 255, 0.3), rgba(52, 109, 255, 0.12));
  box-shadow: 0 18px 45px rgba(3, 12, 29, 0.5), inset 0 1px rgba(255, 255, 255, 0.13);
}

.brand-mark svg { width: 44px; height: 44px; filter: drop-shadow(0 5px 12px rgba(96, 215, 255, 0.3)); }

h1 {
  margin: 0;
  color: var(--text);
  background: none;
  -webkit-text-fill-color: initial;
  font-size: 28px;
  font-weight: 720;
  letter-spacing: -0.04em;
}

.tagline { margin-top: 7px; color: var(--muted); font-size: 12.5px; }
.auth-card { margin: 0 24px; padding: 20px; }
.security-note { margin: 16px 24px 0; color: #71849d; font-size: 11px; text-align: center; }
.security-note span { margin-right: 5px; color: var(--success); font-size: 8px; }

.card {
  margin: 0 14px 14px;
  padding: 17px;
  gap: 11px;
  border: 1px solid var(--border);
  border-radius: 17px;
  background: linear-gradient(155deg, rgba(18, 32, 53, 0.94), rgba(12, 23, 40, 0.94));
  box-shadow: 0 16px 38px rgba(1, 8, 20, 0.25), inset 0 1px rgba(255, 255, 255, 0.025);
}

.top-nav {
  position: relative;
  z-index: 50;
  min-height: 62px;
  margin: 0 0 16px;
  padding: 10px 14px;
  overflow: visible;
  border-bottom: 1px solid rgba(148, 163, 184, 0.1);
  background: rgba(7, 16, 31, 0.72);
  backdrop-filter: blur(18px);
}

.nav-title { font-size: 14px; font-weight: 680; letter-spacing: -0.015em; }
.nav-spacer { width: 34px; }
.nav-actions { align-items: center; gap: 3px; }

.addr-row {
  gap: 9px;
  padding: 5px 7px 5px 4px;
  border-radius: 12px;
}

.addr-row:hover { background: rgba(126, 170, 215, 0.08); }
.account-avatar {
  display: grid;
  place-items: center;
  width: 30px;
  height: 30px;
  flex: 0 0 30px;
  border: 1px solid rgba(97, 215, 255, 0.3);
  border-radius: 10px;
  color: var(--accent);
  background: linear-gradient(145deg, rgba(97, 215, 255, 0.18), rgba(79, 134, 255, 0.1));
  font-size: 12px;
  font-weight: 750;
}

.account-meta { display: flex; min-width: 0; flex-direction: column; gap: 2px; }
.account-meta strong { color: var(--text); font-size: 11px; font-weight: 650; }
.addr-code { color: var(--muted); font: 9.5px/1.15 ui-monospace, SFMono-Regular, Consolas, monospace; }
.chevron { color: #6f839d; font-size: 14px; transition: transform 0.18s ease; }
.chevron-open { transform: rotate(180deg); }

.env-pill {
  padding: 3px 7px;
  border-color: rgba(148, 163, 184, 0.15);
  background: rgba(148, 163, 184, 0.07);
  color: #9eb0c5;
  font-size: 9px;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}
.env-pill-test { border-color: rgba(255, 209, 102, 0.28); color: var(--warning); }
.env-pill-prod { border-color: rgba(85, 230, 165, 0.28); color: var(--success); }

button { -webkit-tap-highlight-color: transparent; }
.btn-primary,
.btn-secondary,
.btn-ghost,
.btn-danger,
.action-btn,
.btn-icon,
.btn-refresh,
.input-action,
.balance-refresh {
  font-family: inherit;
}

.btn-primary,
.btn-secondary,
.btn-ghost {
  min-height: 42px;
  border-radius: 12px;
  font-size: 13px;
  font-weight: 650;
  transition: transform 0.16s ease, border-color 0.16s ease, background 0.16s ease, opacity 0.16s ease, box-shadow 0.16s ease;
}

.btn-primary {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  border: 1px solid rgba(157, 232, 255, 0.22);
  color: #06111f;
  background: linear-gradient(110deg, #76e2ff 0%, #6fc6ff 45%, #769cff 100%);
  box-shadow: 0 10px 24px rgba(55, 145, 255, 0.22), inset 0 1px rgba(255, 255, 255, 0.5);
}
.btn-primary:hover:not(:disabled) { opacity: 1; transform: translateY(-1px); box-shadow: 0 13px 28px rgba(55, 145, 255, 0.28); }
.btn-primary:active:not(:disabled) { transform: translateY(0); }

.btn-secondary {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 7px;
  border-color: rgba(97, 215, 255, 0.25);
  color: #89e4ff;
  background: rgba(61, 157, 210, 0.09);
}
.btn-secondary:hover:not(:disabled) { border-color: rgba(97, 215, 255, 0.5); background: rgba(61, 157, 210, 0.14); }

.btn-ghost {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 7px;
  border-color: var(--border);
  color: var(--muted-strong);
  background: rgba(148, 163, 184, 0.035);
}
.btn-ghost:hover:not(:disabled) { border-color: rgba(148, 197, 231, 0.3); color: var(--text); background: rgba(148, 197, 231, 0.07); }

.btn-primary:disabled,
.btn-secondary:disabled,
.btn-ghost:disabled,
.btn-icon:disabled,
.input-action:disabled,
.balance-refresh:disabled {
  cursor: not-allowed;
  opacity: 0.48;
}

.btn-icon {
  display: inline-grid;
  place-items: center;
  width: 32px;
  height: 32px;
  padding: 0;
  border: 1px solid transparent;
  border-radius: 10px;
  color: #90a4bd;
  background: transparent;
}
.btn-icon svg { width: 16px; height: 16px; fill: none; stroke: currentColor; stroke-linecap: round; stroke-linejoin: round; stroke-width: 1.8; }
.btn-icon:hover:not(:disabled) { border-color: var(--border); color: var(--text); background: rgba(148, 163, 184, 0.07); }
.nav-back { margin-right: auto; }

.lang-toggle {
  top: 14px;
  right: 15px;
  min-width: 36px;
  min-height: 28px;
  border-color: var(--border);
  border-radius: 9px;
  color: var(--muted-strong);
  background: rgba(15, 28, 48, 0.8);
}
.lang-toggle:hover { border-color: var(--border-strong); color: var(--accent); }

.field-group { display: flex; flex-direction: column; gap: 7px; }
.field-grid { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 10px; }
.field-label { margin: 0; color: #9cafc5; font-size: 11px; font-weight: 620; letter-spacing: 0.01em; }
.field-label span { color: #637994; font-size: 9px; font-weight: 500; }

.field-input {
  min-height: 42px;
  padding: 10px 12px;
  border: 1px solid rgba(148, 163, 184, 0.14);
  border-radius: 11px;
  color: var(--text);
  background: rgba(4, 13, 27, 0.55);
  font-size: 12.5px;
  transition: border-color 0.16s ease, box-shadow 0.16s ease, background 0.16s ease;
}
.field-input::placeholder { color: #536882; }
.field-input:hover:not(:disabled) { border-color: rgba(148, 188, 224, 0.26); }
.field-input:focus { border-color: rgba(97, 215, 255, 0.58); background: rgba(4, 14, 29, 0.76); box-shadow: 0 0 0 3px rgba(97, 215, 255, 0.09); }
.field-input:disabled { cursor: not-allowed; opacity: 0.55; }
.mono-input { font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 11.5px; }
select.field-input { color-scheme: dark; }

.tab-bar {
  margin: 0 14px 12px;
  padding: 4px;
  border-color: var(--border);
  border-radius: 13px;
  background: rgba(5, 14, 28, 0.56);
}
.tab { min-height: 34px; border-radius: 9px; color: var(--muted); font-weight: 620; }
.tab-active { color: var(--text); background: rgba(101, 169, 255, 0.13); box-shadow: inset 0 0 0 1px rgba(112, 201, 255, 0.12); }

.mnemonic-box {
  border-color: rgba(97, 215, 255, 0.22);
  border-radius: 12px;
  color: var(--muted);
  background: rgba(4, 13, 27, 0.48);
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
}
.mnemonic-filled { border-color: rgba(97, 215, 255, 0.52); color: #bdefff; }

.balance-card {
  position: relative;
  isolation: isolate;
  margin: 0 14px 14px;
  padding: 20px;
  gap: 9px;
  overflow: hidden;
  border: 1px solid rgba(115, 194, 255, 0.2);
  border-radius: 20px;
  background: linear-gradient(140deg, rgba(21, 49, 79, 0.98), rgba(13, 28, 50, 0.98));
  box-shadow: 0 18px 42px rgba(0, 8, 22, 0.34), inset 0 1px rgba(255, 255, 255, 0.05);
}
.balance-card::after {
  content: '';
  position: absolute;
  z-index: -1;
  width: 170px;
  height: 170px;
  right: -70px;
  top: -90px;
  border-radius: 50%;
  background: rgba(70, 187, 255, 0.18);
  filter: blur(4px);
}
.balance-card-head { display: flex; align-items: center; justify-content: space-between; color: #8fa9c4; font-size: 11px; font-weight: 620; }
.balance-refresh,
.input-action {
  display: grid;
  place-items: center;
  border: 0;
  color: #8fb6d2;
  background: transparent;
  cursor: pointer;
}
.balance-refresh { width: 28px; height: 28px; border-radius: 9px; background: rgba(148, 199, 232, 0.07); }
.balance-refresh:hover:not(:disabled) { color: var(--accent); background: rgba(148, 199, 232, 0.12); }
.balance-refresh svg,
.input-action svg { width: 15px; height: 15px; fill: none; stroke: currentColor; stroke-linecap: round; stroke-linejoin: round; stroke-width: 1.8; }
.balance-main { width: 100%; min-width: 0; gap: 9px; margin: 1px 0 2px; overflow: hidden; }
.balance-val {
  display: block;
  min-width: 0;
  flex: 1 1 auto;
  overflow-x: auto;
  overflow-y: hidden;
  color: #f8fbff;
  font-size: 34px;
  font-weight: 680;
  font-variant-numeric: tabular-nums;
  letter-spacing: -0.045em;
  line-height: 1.15;
  white-space: nowrap;
  scrollbar-width: none;
}
.balance-val::-webkit-scrollbar { display: none; }
.balance-unit { flex: 0 0 auto; color: #88a4bf; font-size: 13px; font-weight: 650; }
.balance-status { color: #7f96b0; }
.balance-status-error { color: #ffbe7a; }
.rent-summary { display: grid; gap: 6px; padding-top: 8px; font-size: 11px; color: var(--muted); }
.rent-summary-row { display: flex; justify-content: space-between; gap: 12px; align-items: baseline; min-width: 0; }
.rent-summary-row span { min-width: 0; }
.rent-summary-row strong { min-width: 0; color: #ccefff; text-align: right; overflow-wrap: anywhere; }
.balance-list { gap: 0; padding-top: 4px; }
.balance-row { padding: 7px 0; border-color: rgba(255, 255, 255, 0.055); color: #8fa2b9; }
.balance-token { display: inline-flex; align-items: center; gap: 7px; color: #dce8f6; max-width: none; min-width: 0; overflow: visible; overflow-wrap: anywhere; white-space: normal; }
.balance-token-icon { width: 18px; height: 18px; flex: 0 0 18px; border-radius: 50%; object-fit: cover; }
.balance-row { align-items: baseline; gap: 12px; }
.balance-row span:last-child { min-width: 0; margin-left: 0; flex-shrink: 1; overflow-wrap: anywhere; text-align: right; }

.action-row { gap: 9px; padding: 0 14px; }
.action-btn {
  min-height: 79px;
  gap: 7px;
  padding: 12px 8px;
  border: 1px solid var(--border);
  border-radius: 16px;
  color: #cbd8e8;
  background: rgba(15, 27, 46, 0.74);
  font-size: 11.5px;
  font-weight: 620;
  box-shadow: inset 0 1px rgba(255, 255, 255, 0.025);
}
.action-btn:hover { transform: translateY(-1px); border-color: rgba(97, 215, 255, 0.32); color: var(--text); background: rgba(20, 37, 61, 0.9); }
.action-icon {
  display: grid;
  place-items: center;
  width: 31px;
  height: 31px;
  border-radius: 11px;
  color: var(--accent);
  background: linear-gradient(145deg, rgba(97, 215, 255, 0.16), rgba(79, 134, 255, 0.09));
}
.action-icon svg { width: 17px; height: 17px; fill: none; stroke: currentColor; stroke-linecap: round; stroke-linejoin: round; stroke-width: 1.8; }

.account-list {
  top: calc(100% + 7px);
  display: flex;
  min-width: 232px;
  max-height: min(420px, calc(100vh - 78px));
  flex-direction: column;
  overflow-x: hidden;
  overflow-y: auto;
  overscroll-behavior: contain;
  border-color: rgba(148, 163, 184, 0.18);
  border-radius: 14px;
  background: rgba(12, 24, 41, 0.98);
  box-shadow: 0 20px 45px rgba(0, 4, 15, 0.56);
  backdrop-filter: blur(18px);
}
.account-item { flex: 0 0 auto; padding: 11px 13px; color: var(--muted); font-family: ui-monospace, SFMono-Regular, Consolas, monospace; }
.account-item:hover { color: var(--text); background: rgba(113, 177, 226, 0.08); }
.account-item-active { color: var(--accent); }
.account-list-manage {
  position: sticky;
  z-index: 2;
  bottom: 0;
  flex: 0 0 auto;
  padding: 11px 13px;
  border-color: var(--border);
  color: var(--accent);
  background: #0d1b2e;
  box-shadow: 0 -10px 22px rgba(5, 14, 29, 0.82);
}
.account-list-manage:hover { background: rgba(97, 215, 255, 0.09); }
.managed-account-row { min-height: 42px; border-color: var(--border); border-radius: 11px; color: var(--muted); background: rgba(4, 13, 27, 0.46); }
.managed-account-row:hover { border-color: var(--border-strong); color: var(--text); }
.managed-account-row-active { border-color: rgba(97, 215, 255, 0.38); color: var(--accent); background: rgba(97, 215, 255, 0.06); }

.send-card { gap: 14px; }
.send-balance-strip { display: flex; align-items: center; justify-content: space-between; margin: -3px -3px 0; padding: 10px 12px; border: 1px solid rgba(97, 215, 255, 0.12); border-radius: 12px; color: var(--muted); background: rgba(97, 215, 255, 0.045); font-size: 11px; }
.send-balance-strip strong { min-width: 0; color: #ccefff; font-size: 12px; font-weight: 680; overflow-wrap: anywhere; text-align: right; }
.send-balance-strip { gap: 12px; }
.send-balance-strip > span { flex-shrink: 0; }
.amount-input-wrap,
.nonce-input-wrap { position: relative; }
.amount-input-wrap { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.amount-input { flex: 1 1 140px; width: 0; min-width: 0; font-size: 18px; font-weight: 650; letter-spacing: -0.02em; }
.amount-input-wrap > span { max-width: 100%; overflow-wrap: anywhere; color: #8298b1; font-size: 11px; font-weight: 700; }
.nonce-input-wrap .field-input { padding-right: 36px; }
.input-action { position: absolute; right: 5px; top: 50%; width: 30px; height: 30px; border-radius: 8px; transform: translateY(-50%); }
.input-action:hover:not(:disabled) { color: var(--accent); background: rgba(97, 215, 255, 0.08); }
.field-help { margin-top: -5px; color: #647a94; font-size: 10px; line-height: 1.45; }
.send-submit { margin-top: 2px; }
.send-submit > svg { width: 17px; height: 17px; fill: none; stroke: currentColor; stroke-linecap: round; stroke-linejoin: round; stroke-width: 2; }

.status-banner {
  padding: 9px 11px;
  border: 1px solid transparent;
  border-radius: 11px;
  font-size: 10.5px;
  line-height: 1.48;
  overflow-wrap: anywhere;
}
.status-error { border-color: rgba(255, 113, 132, 0.2); color: #ff9aaa; background: rgba(255, 113, 132, 0.07); }
.status-warn { border-color: rgba(255, 209, 102, 0.2); color: #f6d989; background: rgba(255, 209, 102, 0.07); }
.status-success { border-color: rgba(85, 230, 165, 0.2); color: #8bf0c1; background: rgba(85, 230, 165, 0.07); }

.send-success-card { align-items: stretch; padding: 24px 18px 18px; text-align: center; }
.success-orb { display: grid; place-items: center; width: 58px; height: 58px; margin: 0 auto 4px; border: 1px solid rgba(85, 230, 165, 0.3); border-radius: 20px; color: var(--success); background: radial-gradient(circle at 35% 25%, rgba(85, 230, 165, 0.2), rgba(44, 145, 118, 0.08)); box-shadow: 0 13px 32px rgba(22, 129, 98, 0.16); }
.success-orb svg { width: 29px; height: 29px; fill: none; stroke: currentColor; stroke-linecap: round; stroke-linejoin: round; stroke-width: 2.2; }
.success-copy { margin-bottom: 4px; }
.success-kicker { color: var(--success); font-size: 9px; font-weight: 750; letter-spacing: 0.12em; text-transform: uppercase; }
.success-copy h2 { margin: 6px 0; color: var(--text); font-size: 20px; font-weight: 720; letter-spacing: -0.035em; }
.success-copy p { color: var(--muted); font-size: 11px; line-height: 1.55; }
.receipt-summary { display: flex; flex-direction: column; overflow: hidden; border: 1px solid var(--border); border-radius: 13px; background: rgba(3, 12, 25, 0.44); text-align: left; }
.receipt-summary > div { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 9px 11px; border-bottom: 1px solid rgba(148, 163, 184, 0.08); }
.receipt-summary > div:last-child { border-bottom: 0; }
.receipt-summary span { color: var(--muted); font-size: 10.5px; }
.receipt-summary strong { min-width: 0; max-width: 100%; color: #dce9f7; font-size: 11px; text-align: right; overflow-wrap: anywhere; }
.receipt-summary code { max-width: 205px; color: #dce9f7; font: 600 10.5px/1.3 ui-monospace, SFMono-Regular, Consolas, monospace; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.success-actions { display: grid; grid-template-columns: 1fr 1fr; gap: 9px; margin-top: 2px; }

.tx-history-item { border-color: var(--border); border-radius: 13px; background: rgba(4, 13, 27, 0.45); }
.mono-mini { color: #778ca5; font-family: ui-monospace, SFMono-Regular, Consolas, monospace; }
.tx-copy-btn { min-height: 25px; }

.hint { color: #7f93ab; line-height: 1.5; }
.msg-error { color: #ff8495; }
.msg-success { color: #69e8af; }
.rent-activation-hint { border-color: rgba(255, 209, 102, 0.22); border-radius: 11px; color: #f2d27c; background: rgba(255, 209, 102, 0.07); }
.danger-card { border-color: rgba(255, 113, 132, 0.2); background: linear-gradient(155deg, rgba(49, 24, 38, 0.72), rgba(25, 18, 31, 0.8)); }
.btn-danger { min-height: 42px; border-color: rgba(255, 113, 132, 0.45); border-radius: 12px; color: #ff8495; background: rgba(255, 113, 132, 0.08); }
.divider { color: #5f748d; }
.divider::before,
.divider::after { content: ''; display: inline-block; width: 36%; height: 1px; margin: 0 8px 3px; background: rgba(148, 163, 184, 0.1); }

.env-switch { padding: 4px; border-color: var(--border); border-radius: 12px; background: rgba(4, 13, 27, 0.45); }
.env-option { min-height: 33px; border-radius: 9px; color: var(--muted); }
.env-option-active { color: var(--text); background: rgba(97, 215, 255, 0.1); box-shadow: inset 0 0 0 1px rgba(97, 215, 255, 0.12); }
.fee-badge,
.approval-primary,
.approval-summary,
.tx-summary { border-color: var(--border); border-radius: 13px; }
.approval-primary { background: linear-gradient(135deg, rgba(97, 215, 255, 0.1), rgba(79, 134, 255, 0.05)); }
.approval-summary { background: rgba(4, 13, 27, 0.48); }
.approval-row span { color: var(--muted); }
.approval-row b { color: var(--text); font-family: ui-monospace, SFMono-Regular, Consolas, monospace; }

.wallet-shell.approval-shell {
  width: 520px;
  min-height: 680px;
}
.approval-shell .page { min-height: 680px; }
.approval-shell .card { margin: 0 18px 18px; padding: 20px; gap: 12px; }
.approval-shell .approval-summary { gap: 9px; padding: 14px 16px; }
.approval-shell .approval-row { gap: 18px; }
.approval-shell .approval-row span { flex: 0 0 126px; }
.approval-shell .approval-row b {
  min-width: 0;
  flex: 1 1 auto;
  overflow-wrap: anywhere;
  word-break: break-word;
}
.approval-shell .approval-subtitle,
.approval-shell .fee-badge,
.approval-shell .hint {
  white-space: normal;
  overflow-wrap: anywhere;
  word-break: break-word;
}

.spinner {
  display: inline-block;
  width: 14px;
  height: 14px;
  flex: 0 0 14px;
  border: 2px solid currentColor;
  border-right-color: transparent;
  border-radius: 50%;
  animation: spin 0.7s linear infinite;
}
.spinning { transform-origin: center; animation: spin 0.8s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }

@media (max-width: 380px) {
  .approval-shell .card { margin: 0 14px 14px; padding: 14px; }
  .approval-shell .approval-summary { padding: 12px; }
  .approval-shell .approval-row { gap: 12px; }
  .approval-shell .approval-row span { flex-basis: 90px; }
}

@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after { scroll-behavior: auto !important; transition-duration: 0.01ms !important; animation-duration: 0.01ms !important; animation-iteration-count: 1 !important; }
}
</style>
