                                                                           
                                                                   
import { createHash } from 'node:crypto'
import { mkdir, writeFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { resolve } from 'node:path'
import { getAddress } from 'ethers'

const root = resolve(fileURLToPath(new URL('../..', import.meta.url)))
const output = resolve(root, 'shared/top200TokenIcons.mjs')
const imageDir = resolve(root, 'shared/assets/top200-token-icons')
const snapshotFile = resolve(root, 'shared/top200TokenIcons.snapshot.json')
const rankingURL = 'https://api.coingecko.com/api/v3/coins/markets?vs_currency=usd&order=market_cap_desc&per_page=200&page=1&sparkline=false'
const platformsURL = 'https://api.coingecko.com/api/v3/coins/list?include_platform=true'
const trustRoot = 'https://raw.githubusercontent.com/trustwallet/assets/master/blockchains'
const chains = [
  { cg: 'ethereum', code: 'eth', trust: 'ethereum' },
  { cg: 'binance-smart-chain', code: 'bnb', trust: 'smartchain' },
  { cg: 'base', code: 'base', trust: 'base' },
  { cg: 'arbitrum-one', code: 'arb', trust: 'arbitrum' },
  { cg: 'tron', code: 'trx', trust: 'tron' },
]

async function get(url) {
  for (let attempt = 0; attempt < 3; attempt++) {
    let response
    try {
      response = await fetch(url, { headers: { 'User-Agent': 'dex-top200-icon-snapshot' } })
    } catch (error) {
      if (attempt === 2) throw error
      await new Promise(done => setTimeout(done, 1000 * (attempt + 1)))
      continue
    }
    if (response.ok) return response
    if (response.status === 404) return null
    if (attempt === 2 || (response.status !== 429 && response.status < 500)) throw new Error(`${url}: HTTP ${response.status}`)
    await new Promise(done => setTimeout(done, 1000 * (attempt + 1)))
  }
  return null
}

function contractFor(chain, value) {
  if (chain.code === 'trx') return /^T[1-9A-HJ-NP-Za-km-z]{33}$/.test(value) ? value : ''
  if (!/^0x[\da-fA-F]{40}$/.test(value)) return ''
  return getAddress(value)
}

function safeName(value) {
  return String(value).replace(/[^a-z0-9-]/gi, '-').toLowerCase()
}

const [rankingResponse, platformsResponse] = await Promise.all([get(rankingURL), get(platformsURL)])
if (!rankingResponse || !platformsResponse) throw new Error('ranking or platform list unavailable')
const rankingRaw = await rankingResponse.text()
const ranking = JSON.parse(rankingRaw)
const platforms = new Map((await platformsResponse.json()).map(entry => [entry.id, entry.platforms || {}]))
if (ranking.length !== 200) throw new Error(`expected 200 ranked coins; got ${ranking.length}`)
const capturedAt = new Date().toISOString()
const marketSha256 = createHash('sha256').update(rankingRaw).digest('hex')
const candidates = []
for (const coin of ranking) {
  for (const chain of chains) {
    const contract = contractFor(chain, String(platforms.get(coin.id)?.[chain.cg] || ''))
    if (contract) candidates.push({ coin, chain, contract })
  }
}

let cursor = 0
const accepted = []
const skipped = []
const imageCandidates = []
await mkdir(imageDir, { recursive: true })
async function worker() {
  while (cursor < candidates.length) {
    const { coin, chain, contract } = candidates[cursor++]
    const assetURL = `${trustRoot}/${chain.trust}/assets/${contract}`
    try {
      const infoResponse = await get(`${assetURL}/info.json`)
      if (!infoResponse) { skipped.push({ id: coin.id, chain: chain.code, contract, reason: 'no Trust Wallet asset record' }); continue }
      const info = await infoResponse.json()
      if (String(info.symbol || '').toLowerCase() !== coin.symbol.toLowerCase()) {
        skipped.push({ id: coin.id, chain: chain.code, contract, reason: `symbol mismatch: ${info.symbol || ''}` }); continue
      }
      const logoResponse = await get(`${assetURL}/logo.png`)
      if (!logoResponse) { skipped.push({ id: coin.id, chain: chain.code, contract, reason: 'no localizable logo' }); continue }
      const logo = Buffer.from(await logoResponse.arrayBuffer())
      if (logo.length > 300_000 || logo.length < 100 || logo.subarray(0, 8).toString('hex') !== '89504e470d0a1a0a') {
        skipped.push({ id: coin.id, chain: chain.code, contract, reason: 'invalid or oversized PNG' }); continue
      }
      const iconFile = `${safeName(coin.id)}.png`
      imageCandidates.push({ iconFile, logo, priority: chains.indexOf(chain) })
      accepted.push({ rank: coin.market_cap_rank, id: coin.id, symbol: coin.symbol.toUpperCase(), chain: chain.code,
        contract: chain.code === 'trx' ? contract : contract.toLowerCase(), icon: iconFile,
        assetSource: `${assetURL}/info.json`, iconSource: `${assetURL}/logo.png` })
    } catch (error) {
      skipped.push({ id: coin.id, chain: chain.code, contract, reason: String(error) })
    }
  }
}
await Promise.all(Array.from({ length: 8 }, worker))
for (const iconFile of [...new Set(imageCandidates.map(item => item.iconFile))].sort()) {
  const image = imageCandidates.filter(item => item.iconFile === iconFile).sort((a, b) => a.priority - b.priority)[0]
  await writeFile(resolve(imageDir, iconFile), image.logo)
}
                                                                        
                                                                             
const officialExtras = [
  { id: 'uniswap', chain: 'base', contract: '0xc3De830EA07524a0761646a6a4e4be0e114a3C83' },
  { id: 'uniswap', chain: 'bnb', contract: '0x06e8bdE95BE4ce5cB1134BD47aD18a79fFB35822' },
]
for (const extra of officialExtras) {
  const coin = ranking.find(item => item.id === extra.id)
  const image = accepted.find(item => item.id === extra.id)
  const contract = extra.contract.toLowerCase()
  if (!coin || !image || accepted.some(item => item.chain === extra.chain && item.contract === contract)) continue
  accepted.push({ rank: coin.market_cap_rank, id: coin.id, symbol: coin.symbol.toUpperCase(), chain: extra.chain,
    contract, icon: image.icon,
    assetSource: 'https://developers.uniswap.org/docs/ecosystem/governance/uni', iconSource: image.iconSource })
}
if (accepted.length < 120 || skipped.some(item => /^TypeError|^Error: .*HTTP/.test(item.reason))) {
  throw new Error('snapshot incomplete; review upstream failures before replacing the catalog')
}
accepted.sort((a, b) => a.rank - b.rank || a.chain.localeCompare(b.chain) || a.contract.localeCompare(b.contract))
skipped.sort((a, b) => a.id.localeCompare(b.id) || a.chain.localeCompare(b.chain))
const icons = [...new Set(accepted.map(entry => entry.icon))].sort()
                                                                           
                                                                               
const entries = accepted.map(({ rank, id, symbol, chain, contract, icon }) => ({ rank, id, symbol, chain, contract, icon }))
const moduleText = `// Generated by explorer/scripts/snapshot-top200-token-icons.mjs on ${capturedAt}\n` +
  `// Market ranking: CoinGecko; exact address and logo: Trust Wallet assets. UI only.\n` +
  `import { nativeKey } from './assetMetadata.mjs'\n\n` +
  `const ICONS = import.meta.glob ? import.meta.glob('./assets/top200-token-icons/*.png', { eager: true, query: '?url', import: 'default' }) : {}\n\n` +
  `export const TOP_200_TOKEN_ICONS = ${JSON.stringify(entries, null, 2)}\n\n` +
  `const BY_NATIVE = new Map(TOP_200_TOKEN_ICONS.map(asset => [nativeKey(asset.chain, asset.contract), asset]))\n` +
  `export function top200AssetForNative(chain, contract) {\n` +
  `  return BY_NATIVE.get(nativeKey(chain, contract))\n` +
  `}\n` +
  `export function top200IconForNative(chain, contract) {\n` +
  `  const asset = top200AssetForNative(chain, contract)\n` +
  `  return asset ? ICONS[\`./assets/top200-token-icons/\${asset.icon}\`] || '' : ''\n` +
  `}\n`
await writeFile(output, moduleText)
await writeFile(snapshotFile, JSON.stringify({ capturedAt, rankingURL, platformsURL, marketSha256,
  top200: ranking.map(coin => ({ rank: coin.market_cap_rank, id: coin.id, symbol: coin.symbol, name: coin.name })),
  accepted, skipped }, null, 2) + '\n')
console.log(JSON.stringify({ capturedAt, marketSha256, ranking: ranking.length, candidates: candidates.length,
  accepted: accepted.length, uniqueCoins: new Set(accepted.map(entry => entry.id)).size, icons: icons.length,
  skipped: skipped.length }))
