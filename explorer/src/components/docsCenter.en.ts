export type EnglishDocPage = {
  id: string
  title: string
  summary: string
  params?: { name: string; value: string }[]
  sections: { heading: string; level?: 2 | 3; body?: string; bullets?: string[]; code?: string; codeLabel?: string }[]
}

export const englishDocs: EnglishDocPage[] = [
  {
    id: 'docs-home',
    title: 'Documentation Overview',
    summary: 'Guidance for traders, cross-chain users, witness miners, and node miners on using the system, participating in mining, understanding risk, and diagnosing unexpected behavior.',
    params: [
      { name: 'Audience', value: 'Everyday users, traders, witness miners, and node miners' },
      { name: 'Recommended order', value: 'Start with Quick Start, then continue to accounts, trading, cross-chain operations, or the relevant miner role' },
      { name: 'Core goal', value: 'Reduce mistakes, clarify rewards and risks, and give users and miners a consistent troubleshooting standard' },
    ],
    sections: [
      { heading: 'What is covered', bullets: ['Accounts, wallets, assets, rent, and activation fees.', 'Spot and perpetual trading, funding, liquidation, and fees.', 'Deposits, withdrawals, witness mining, node mining, rewards, and operational safety.'] },
      { heading: 'Purpose', body: 'The documentation focuses on information users can see, actions they can take, and signals they need to interpret during routine use and troubleshooting.' },
      { heading: 'Version and network', body: 'This handbook covers the public Explorer and the associated wallet and node workflows. Before deploying, review the target network release notes and check which flags your binary supports. Fees, stakes, and block intervals follow the selected network and transaction confirmation.' },
      { heading: 'Start with a task', bullets: ['Begin with [First Use](#docs_center/first-use) to connect wallets, prepare funds, and complete an operation.', 'Before running a node, read [Hardware](#docs_center/node-mining-hardware) and then [Deployment](#docs_center/node-mining-deploy-join).', 'For an uncertain outcome, use [Troubleshooting](#docs_center/faq) and keep the original transaction or request ID.', 'Search includes article bodies. Copy link shares the current article; switching languages keeps it selected.'] },
    ],
  },
  {
    id: 'parameter-cheatsheet',
    title: 'Parameter Cheat Sheet',
    summary: 'Frequently used thresholds, rates, and intervals. If a value shown in the product differs, the current transaction confirmation and live network value take precedence.',
    params: [
      { name: 'Minimum stake', value: '32 FB for node-mining or witness-mining admission' },
      { name: 'Rent-free threshold', value: 'At least 10 nominal FB, or any one of the 15 genesis assets meeting its fixed quantity threshold (about 10 USD when defined)' },
      { name: 'Rent rate', value: '100 minimum FB units per block, about 0.000001 FB' },
      { name: 'Activation fee', value: '0.01 FB when a non-FB transfer leaves the recipient below every rent exemption threshold with zero nominal FB' },
      { name: 'Taker fee', value: '5 bps = 0.05%' },
      { name: 'Maker fee', value: '2 bps = 0.02%' },
      { name: 'Referral', value: 'Referrer 20%; referee 10% of Full Fee' },
      { name: 'Maximum leverage', value: '100x' },
      { name: 'Maint. margin', value: '50 bps = 0.5%' },
      { name: 'Funding-rate cap', value: '±75 bps, or ±0.75% per settlement' },
      { name: 'Funding interval', value: '10 blocks by default; the live value takes precedence' },
      { name: 'Active miner offline grace', value: '17,280 blocks from the first height with sufficient offline evidence' },
      { name: 'Vault committee term', value: '120,960 blocks for modes with automatic rotation enabled' },
      { name: 'Challenge funding', value: 'At least 10 FB per contribution; admission requires max(10 FB, 10% of this case’s original witness reserves)' },
      { name: '51025 deposit request', value: 'Prepay at least 0.01 FB in witness fees; the submitter separately locks a 10 FB bond, returned on final PASS and forfeited on final FAIL' },
      { name: '51025 successful deposit rewards', value: 'The request submitter and eligible PASS witnesses each receive 0.01% of gross; the receiver gets the remainder' },
    ],
    sections: [
      { heading: 'Units', bullets: ['1 bp (basis point) equals 0.01%.', 'A block is an on-chain unit, not a fixed number of seconds.', 'Full Fee means the fee before discounts or rebates.'] },
      { heading: 'How to use these values', body: 'Use this page for planning. Before submitting an action, verify the confirmation dialog, order details, position page, and cross-chain status page.' },
    ],
  },
  {
    id: 'roles-entry',
    title: 'Roles and Entry Points',
    summary: 'Each role needs a different view: users focus on assets, witness miners on cross-chain verification, and node miners on consensus, block production, and synchronization.',
    params: [
      { name: 'Everyday user', value: 'Wallet, assets, transfers, and deposit or withdrawal progress' },
      { name: 'Trader', value: 'Spot, perpetuals, orders, positions, funding, and liquidation risk' },
      { name: 'Witness miner', value: 'Deposit witness tasks, votes, challenges, stake, and rewards' },
      { name: 'Node miner', value: 'Node availability, consensus, block production, synchronization, stake, and rewards' },
    ],
    sections: [
      { heading: 'User entry points', bullets: ['Confirm the connected account, network, and balances before acting.', 'Review pair, price, quantity, fees, and expected slippage before trading.', 'Verify chain, asset, destination address, and processing stage before a cross-chain action.'] },
      { heading: 'Miner entry points', body: 'Witness miners begin with assigned witness tasks and stake status. Node miners begin with node health, synchronization, and recent block activity. The roles have separate admission and availability requirements.' },
    ],
  },
  {
    id: 'core-concepts',
    title: 'Core Concepts',
    summary: 'The essential concepts behind accounts, transactions, blocks, order books, positions, cross-chain vaults, and finality.',
    params: [
      { name: 'Account', value: 'An fbc1 identity with balances, nonce, orders, positions, and activity' },
      { name: 'Finality', value: 'The authoritative result after a transaction is included in a finalized block' },
      { name: 'Risk', value: 'Possible balance, margin, stake, or cross-chain loss caused by an action or protocol outcome' },
      { name: 'bps', value: 'Basis points; 1 bps equals 0.01%' },
    ],
    sections: [
      { heading: 'Accounts and state', bullets: ['A signed transaction expresses an action by an account.', 'Balances, orders, positions, stake, and protocol records form the current state.', 'The account nonce orders user-authorized actions and prevents duplicate execution.'] },
      { heading: 'Transactions and finality', body: 'A submitted transaction is not necessarily successful. Use its final receipt and finalized block to determine the authoritative result.' },
      { heading: 'Cross-chain and vaults', body: 'Deposits, withdrawals, vault jobs, and distributed signatures have their own state transitions. Always use the relevant request detail and finalized status.' },
    ],
  },
  {
    id: 'first-use',
    title: 'First Use: Complete Your First Operation',
    summary: 'Connect a wallet, prepare fees, submit one operation, and verify the final result. Start with a DEX transfer or spot order before using cross-chain or perpetual features.',
    params: [
      { name: 'DEX identity', value: 'Your fbc1… address in FrostBit Wallet' },
      { name: 'Two fee balances', value: 'FB for DEX transactions; the external network currency or resources for external transactions' },
      { name: 'Completion check', value: 'A successful final receipt and the expected balance, order, or deposit record' },
    ],
    sections: [
      {
        heading: '1. Connect your DEX wallet',
        bullets: [
          'Install and enable the FrostBit Wallet browser extension, create or restore your account inside the wallet, and keep your backup private. Obtain the wallet only from a project release channel you have verified.',
          'Click “Connect Wallet”, approve the expected site in the extension, and compare the displayed fbc1… address with your intended account.',
          'An external wallet pays for deposits, reads token details, or claims withdrawals on the external chain. It does not replace FrostBit Wallet for DEX transactions.',
        ],
      },
      {
        heading: '2. Prepare FB and the asset you need',
        bullets: [
          'Receive FB from an existing DEX account at your fbc1… address, then check the available balance in Account Center. External ETH, TRX, or stablecoins cannot pay DEX runtime fees.',
          'Keep enough available FB for the displayed runtime fee. Account rent can also apply; see Account Rent for exemptions and renewal behavior.',
          'To bring in an external asset, open Bridge → Deposit and follow Deposit Flow. An external payment must reach final DEX credit before its balance can fund an order.',
        ],
      },
      {
        heading: '3. Read a spot limit order',
        bullets: [
          'Open the spot market under Spot / Futures. Select an available pair whose quote asset you hold. For BTC / ETH.USDT, price is ETH.USDT per BTC and quantity is BTC.',
          'Illustrative numbers only: a price of 50,000 and a buy quantity of 0.001 give a notional amount of 50 ETH.USDT. Review trading fees and the FB runtime fee separately. This is not a live quote or a suggested order.',
          'Check side, quantity, and fees, sign in the DEX wallet, and inspect the transaction and order. An unfilled order locks funds; a partial fill affects only the executed portion, and you can cancel the remaining order.',
        ],
      },
      {
        heading: '4. Save evidence and verify the result',
        bullets: [
          'Save the DEX transaction ID. For cross-chain operations, also save the external deposit hash or withdrawal ID. A connected wallet, signature, or submission acknowledgement is not final success.',
          'For orders, check filled and remaining quantity. For deposits, check final DEX credit. For account-chain withdrawals, check successful Vault execution and the external-chain record.',
          'If status is uncertain, inspect the original record first. Retry after confirming failure or that nothing was submitted, so you do not pay or place an order twice.',
        ],
      },
    ],
  },
  {
    id: 'wallet-extension',
    title: 'Install and Use the Wallet Extension',
    summary: 'Install FrostBit Wallet in Chrome or Edge, create or restore an account, and connect to Explorer to review signing requests.',
    sections: [
      {
        heading: 'Download and install',
        bullets: [
          'Download and extract `frostbit-wallet-<version>.zip` from [Frostbit Releases](https://github.com/commonstate-labs/frostbit/releases/latest). The full Linux release also includes a `wallet/` directory.',
          'Open `chrome://extensions/` in Chrome or `edge://extensions/` in Edge. Turn on Developer mode, choose Load unpacked, and select the extracted `wallet` directory that contains `manifest.json`. Open FrostBit Wallet from the toolbar.',
          'Before updating, check your recovery phrase and private key backups. Update the files in the existing installation directory, then select Reload on the extensions page. Changing the directory can create a new extension ID.',
        ],
      },
      {
        heading: 'Build from source',
        body: 'Download `frostbit-<version>-source.zip` from the same release and enter the extracted source root that contains `go.mod`. Run these commands with Node.js 22.12 or a later 22.x version, then use Load unpacked to select `wallet/dist/`.',
        codeLabel: 'Bash',
        code: `npm --prefix wallet ci
npm --prefix wallet run build`,
      },
      {
        heading: 'Create, restore, and connect',
        bullets: [
          'Open the extension and select Create / Import Wallet. To create a wallet, generate and back up the recovery phrase offline, then set a wallet password. To restore, import the recovery phrase or use the private key import to restore an account backed up separately. Enter credentials only in the extension you have verified. Compare the full `fbc1…` address to confirm the account.',
          'In wallet Settings, choose the network and save its Explorer URL if needed. The extension supports the [FrostBit website](https://frostbit.io/) and local pages on `http://localhost` or `http://127.0.0.1`, such as `http://127.0.0.1:8080`.',
          'Unlock the wallet, refresh the page, and select Connect Wallet. Check the requesting site before approving the connection. Before signing, review the network, signer, action, recipient, asset, amount, and fees. Save the TxID after submission and check the final result in Explorer. See [Connect a Wallet](#docs_center/wallet-connect).',
        ],
      },
    ],
  },
  {
    id: 'wallet-connect',
    title: 'Connect a Wallet',
    summary: 'Your DEX wallet provides identity and transaction signatures. An external wallet pays for deposits or claims withdrawals on the selected external network. Verify both accounts and networks.',
    params: [
      { name: 'DEX wallet', value: 'FrostBit Wallet with an fbc1… account address' },
      { name: 'External wallet', value: 'Choose a wallet offered for the selected chain and verify its account and network' },
      { name: 'Private credentials', value: 'Handle seed phrases, private keys, and passwords only in your trusted wallet' },
    ],
    sections: [
      {
        heading: 'Connect and verify',
        bullets: [
          'Enable and unlock the FrostBit Wallet browser extension, click “Connect Wallet”, and approve the intended site inside the extension.',
          'Compare the full fbc1… address. Before signing a transfer, order, stake, or DEX cross-chain request, review action, asset, quantity, recipient, and fees.',
          'In deposit or account-chain withdrawal steps, use the external-wallet selector to choose a wallet supported for that network. Follow the displayed guidance if it is absent, locked, or incompatible.',
          'The same 0x address can exist on several EVM networks. Check Ethereum, BNB Chain, Base, and Arbitrum separately, and use the correct network and address for TRON or BTC.',
        ],
      },
      {
        heading: 'The accounts can differ',
        body: 'For a deposit, the DEX recipient determines who receives the credit and the external wallet pays the principal and network fee. For an account-chain withdrawal, the recipient is fixed in the request. The wallet paying the claim fee may differ from the recipient; changing that wallet does not redirect the payment.',
      },
      {
        heading: 'Recover a failed connection',
        bullets: [
          'If the wallet is missing, enable and unlock a version of FrostBit Wallet that supports frostbit.io, allow access to the site, and refresh.',
          'Wrong account: select the intended account inside the wallet, reconnect, and check address and balance.',
          'No signing response: check for an existing confirmation popup before clicking again.',
          'If the prompt says it will only sign, approval returns the signature to the requesting page, which handles submission. When a transfer from the wallet reports “Transaction admitted to the node pool”, use its TxID to check execution on-chain.',
          'Nonce is the account transaction sequence. If there is a conflict, check whether an earlier transaction is still pending, then use “Refresh nonce” on the transfer page and keep the value returned by the node.',
          'After a rejected signature or a timeout, check whether the original transaction was submitted. Look up its TxID if available and retry only after confirming no submission or a failed execution.',
        ],
      },
    ],
  },
  {
    id: 'asset-address',
    title: 'Assets and Addresses',
    summary: 'Understand the difference between the native DEX account address and external-chain deposit or withdrawal addresses.',
    params: [
      { name: 'DEX address', value: 'An fbc1 address used for DEX accounts and transaction signatures' },
      { name: 'External-chain address', value: 'A native BTC or EVM address used for deposits, withdrawals, and vault change' },
      { name: 'Asset identity', value: 'Consensus binds the normalized chain and native contract; symbol, decimals, and icons are local UI metadata' },
    ],
    sections: [
      { heading: 'Address rules', bullets: ['DEX account addresses use the fbc1 prefix.', 'BTC and EVM addresses remain native external-chain addresses.', 'Never substitute a DEX address for an external-chain destination.'] },
      { heading: 'Balance display', body: 'Balances may include available, frozen, position margin, or cross-chain assets. Check the asset detail before assuming the full displayed amount is spendable.' },
    ],
  },
  {
    id: 'account-rent',
    title: 'Account Rent',
    summary: 'Eligible fbc1 accounts are rent-free when nominal FB or one genesis asset meets its threshold. Otherwise keep an active FB rent period; rent is measured by block height.',
    params: [
      { name: 'Rent-free threshold', value: '10 nominal FB, or one genesis asset meeting its own threshold; do not combine different assets' },
      { name: 'Genesis thresholds', value: 'BTC 0.00013; ETH on each supported chain 0.004; BNB 0.014; TRX 30; each genesis USDT / USDC / DAI asset 10' },
      { name: 'Measurement', value: 'Fixed quantities, not live USD values; count available, frozen, and supported owned collateral for the same asset. Each chain is separate.' },
      { name: 'Rent rate', value: '100 minimum FB units per block, about 0.000001 FB' },
      { name: 'Nominal FB', value: 'FB still owned across balance buckets, stake, margin, and supported claims' },
      { name: 'User focus', value: 'Rent status, spendable FB, pending rent, required funding, and recycle height' },
    ],
    sections: [
      { heading: 'What counts', bullets: ['Any one of the 15 genesis assets meeting its threshold stops new rent accrual. 6 USDT plus 6 USDC does not combine into a 12 USD exemption. Incoming FB first pays rent already owed.', 'An active FB rent period does not cancel orders or convert assets. When rent needs funding, unfilled spot and futures orders are cancelled to release frozen funds before limited attempts to buy FB through a direct market.', 'Conversion uses the previous block reference price with a 5% slippage bound and renews rent only with FB actually received. No bids, direct market, reference price, or acceptable price means the existing rent and cleanup rules continue.'] },
      {
        heading: 'Reading balances and funding rent',
        bullets: [
          'Open Account from Trading and find the rent panel labeled 账户租金安全. Spendable FB deducts projected rent from recorded available FB at the displayed height. The balance is settled when account funds move or at the rent processing height.',
          'Pending rent is the accrued amount due. Unpaid rent is what remains after available FB is applied. Required funding may also include the balance needed to renew rent. Use spendable FB when checking amounts and fees.',
          'If the account needs funding, add the required FB before cleanup starts, then verify rent status and spendable balance. Meeting a rent-free threshold stops new rent accrual. Prior rent still has to be paid.',
        ],
      },
      { heading: 'Cleanup impact', body: 'If conversion still cannot fund rent, the account is cleared in batches and remaining assets, including unconverted assets, move to the protocol recycle account. Transfers cannot restore an account during cleanup. Cancelling futures orders does not release margin of an existing position. Positions and other business assets follow account cleanup rules. If the address has no live state, check its activity history for a rent cleanup entry (RENT_SWEEP) to confirm a prior rent cleanup.' },
    ],
  },
  {
    id: 'activation-fee',
    title: 'Account Activation Fee',
    summary: 'When a non-FB transfer leaves an fbc1 address with zero nominal FB below the exemption thresholds, the sender transfers an additional 0.01 FB to fund rent. The same rule applies to existing accounts.',
    params: [
      { name: 'Trigger', value: 'A non-FB transfer to an fbc1 address with zero nominal FB that remains below the rent exemption thresholds after receiving the asset' },
      { name: 'Activation fee', value: '0.01 FB' },
      { name: 'Nature', value: 'FB transferred to the recipient for rent activation, not a hidden trading fee' },
      { name: 'Important', value: 'The sender must have enough FB for both activation and transaction fees' },
    ],
    sections: [
      { heading: 'When it applies', body: 'The incoming amount counts toward the recipient’s total for that same genesis asset. If any rent exemption threshold is met after receipt, activation funding is unnecessary. Existing rent debt still follows the account rent rules.' },
      { heading: 'Before sending', body: 'Verify that the sender has enough FB for both transaction fees and activation.' },
    ],
  },
  {
    id: 'spot-trading',
    title: 'Spot Trading',
    summary: 'Spot orders exchange assets directly. Verify pair direction and quantity units, then reconcile orders, fills, and account balances.',
    params: [
      { name: 'Entry', value: 'Spot / Futures → Spot Trading' },
      { name: 'Taker fee', value: '5 bps = 0.05%; use the current confirmation' },
      { name: 'Maker fee', value: '2 bps = 0.02%; use the current confirmation' },
      { name: 'Balances', value: 'Available funds can fund orders; resting orders hold frozen funds' },
    ],
    sections: [
      {
        heading: 'Check before submitting',
        bullets: [
          'In BTC / ETH.USDT, quantity is BTC and the quoted payment asset is ETH.USDT. Assets from another chain, such as BNB.USDT, are separate balances.',
          'Select buy or sell, enter limit price and quantity, and review notional value, fees, available funds, and the FB runtime fee.',
          'A limit order can execute immediately against suitable orders, fill partially, or wait on the book. Use actual fills to determine execution price and quantity.',
        ],
      },
      {
        heading: 'Example: a 50 ETH.USDT buy order',
        bullets: [
          'For arithmetic only, ignoring fees and price changes: buying 0.001 BTC at 50,000 ETH.USDT per BTC has a notional value of 50 ETH.USDT. This is not a live market quote.',
          'If 0.0004 BTC fills at that price, the filled notional is 20 ETH.USDT. The remaining 0.0006 BTC reserves a notional 30 ETH.USDT. Actual debits and reservations also reflect the displayed fees.',
          'Find the original order and cancel its remaining 0.0006 BTC. Cancellation does not reverse the 0.0004 BTC already bought. After final confirmation, check that the remaining funds have been released.',
        ],
      },
      {
        heading: 'Verify after submission',
        bullets: [
          'Use transaction details to check final success or failure, and order details for cumulative fills, remaining quantity, and order state.',
          'If available funds fall without a full fill, inspect frozen order funds. Do not place another order simply to refresh the display.',
          'Cancellation is another transaction. A fill that executes first can reduce what remains cancellable; reconcile final fills, the cancellation receipt, and account activity.',
        ],
      },
    ],
  },
  {
    id: 'futures-trading',
    title: 'Perpetual Futures',
    summary: 'Perpetual positions use margin to absorb profit and loss. Distinguish frozen order funds, position margin, funding, and realized PnL. Numbers below only explain the display.',
    params: [
      { name: 'Entry', value: 'Spot / Futures → Perpetual Futures' },
      { name: 'Leverage limit', value: 'The current form allows up to 100x; this is a limit, not a recommendation' },
      { name: 'Maintenance margin', value: '50 bps = 0.5% by default; check the current network' },
      { name: 'Core indicators', value: 'Quantity, margin, mark price, unrealized PnL, funding, and liquidation price' },
    ],
    sections: [
      {
        heading: 'Orders and reservations',
        bullets: [
          'Choose the pair and BUY / SELL direction, enter price, quantity, and leverage, and review available quote-asset margin and all fees.',
          'Unfilled opening orders reserve frozen funds; executed portions fund position margin. Cancelling a remaining order does not close the position created by its fills.',
          'Trading fees, FB runtime fees, funding, and margin are separate. Current-window funding displayed on screen is not necessarily an amount still awaiting deduction; use actual margin and position health to assess risk.',
        ],
      },
      {
        heading: 'Example: adding margin',
        bullets: [
          'Static arithmetic only: a 0.01 BTC position marked at 50,000 ETH.USDT per BTC has a notional value of 500 ETH.USDT. If margin is 100 ETH.USDT and you add 20, with no intervening fills, fees, or funding, margin becomes 120 while quantity remains 0.01 BTC.',
          'Click “Add Margin” on that position, enter the amount, and sign with the DEX wallet. After final success, reconcile the decrease in available balance with the increase in margin.',
          'Adding margin changes neither direction nor existing losses. Removing margin reduces the buffer. Liquidation price and health can continue to change with the market.',
        ],
      },
      {
        heading: 'Closing and partial execution',
        bullets: [
          'The current position-card “Close” action submits a market close for the entire remaining position. “Close All” submits requests for multiple positions in sequence; it does not guarantee simultaneous or equal-price execution.',
          'If a request to close 0.01 BTC only fills 0.004 BTC, inspect the remaining 0.006 BTC position and order. A submitted close request does not prove the position is zero.',
          'Before acting again, confirm the previous close transaction and current remaining quantity. The current page has no separate input for choosing a partial-close quantity.',
          'After a full close, inspect Closed Positions. Reconcile returned funds with realized PnL, trading fees, and funding rather than assuming the original margin is returned unchanged.',
        ],
      },
    ],
  },
  {
    id: 'funding-rate',
    title: 'Funding Rate',
    summary: 'Funding periodically transfers value between long and short positions to keep perpetual prices aligned with the reference market.',
    params: [
      { name: 'Funding interval', value: 'Currently 10 blocks by default; use the live network value' },
      { name: 'Funding-rate cap', value: '±75 bps = ±0.75% per settlement' },
      { name: 'Display basis', value: 'The current-window estimate is for display; settled funding is authoritative' },
      { name: 'User impact', value: 'Funding may increase or reduce position margin when settled' },
      { name: 'Miner focus', value: 'Watch periodic funding application, liquidation work, and block-time impact' },
    ],
    sections: [
      { heading: 'How it affects a position', bullets: ['The sign of the rate determines which side pays.', 'Settlement is based on open position size and the applicable rate.', 'Pending funding affects effective margin only after the protocol-defined settlement logic.'] },
      { heading: 'Display versus settlement', body: 'The page may show an estimate for the current window. Use settled funding and position details for the authoritative amount.' },
    ],
  },
  {
    id: 'liquidation-risk',
    title: 'Liquidation Risk',
    summary: 'A position may be liquidated when its effective margin can no longer satisfy maintenance requirements.',
    params: [
      { name: 'Triggers', value: 'Mark-price movement, insufficient effective margin, funding, or liquidation execution' },
      { name: 'Maintenance margin', value: '50 bps = 0.5% by default' },
      { name: 'Maximum leverage', value: 'Up to 100x; higher leverage reduces the safety buffer' },
      { name: 'User action', value: 'Reduce exposure, add margin when supported, or close before the threshold' },
      { name: 'Miner action', value: 'Watch liquidation hooks, insurance-fund accounting, and conservation audits' },
    ],
    sections: [
      { heading: 'Warning signals', bullets: ['Mark price approaches the liquidation price.', 'Margin ratio deteriorates or available margin becomes small.', 'Large pending funding or fast market movement increases risk.'] },
      { heading: 'Risk reduction', body: 'Reduce position size, add margin when supported, or close the position before the risk threshold is reached. Liquidation can execute across multiple price levels.' },
      {
        heading: 'Insurance-covered deleveraging and ADL',
        bullets: [
          'The protocol may reduce a profitable position when a counterparty liquidation leaves a shortfall. Activity records show insurance-covered deleveraging when the insurance fund pays the full profit due. They show ADL when some profit is withheld.',
          'The current cap is 80% of the gross price profit on each portion reduced by ADL. In the Closed section, compare gross price PnL, unpaid ADL profit, and net realized price PnL. Funding and trading fees are reported separately.',
        ],
      },
    ],
  },
  {
    id: 'fees-rebates',
    title: 'Fees and Referral Rebates',
    summary: 'Trading fees depend on maker/taker execution. Referral discounts and rebates are calculated from the full fee.',
    params: [
      { name: 'Taker fee', value: '0.05%' },
      { name: 'Maker fee', value: '0.02%' },
      { name: 'Referrer rebate', value: '20% of Full Fee' },
      { name: 'Referee discount', value: '10% of Full Fee' },
      { name: 'Other fee sources', value: 'Runtime fees, funding, activation, and protocol-specific actions' },
      { name: 'Where to verify', value: 'Transaction, fill, fee, referral, and account-activity records' },
      { name: 'Important', value: 'Actual execution and final receipts take precedence over estimates' },
    ],
    sections: [
      { heading: 'User verification', body: 'The final fee is determined from actual fills, not only from the submitted order. Use transaction, trade, fee, and referral history to verify every charge, discount, and rebate.' },
    ],
  },
  {
    id: 'deposit-flow',
    title: 'Deposit Flow',
    summary: 'BTC uses a deposit address bound to the DEX recipient. Ethereum, BNB Chain, Base, Arbitrum, and TRON use recipient-bound contract deposits. External payment is followed by witnessing, a challenge window, and final DEX credit.',
    params: [
      { name: 'Entry', value: 'Bridge → Deposit' },
      { name: 'BTC evidence', value: 'txid plus the vout of the deposit output' },
      { name: 'Account-chain evidence', value: 'External transaction hash and deposit event binding recipient, asset, and amount' },
      { name: 'Completion', value: 'RECHARGE_FINALIZED with confirmed account credit' },
      { name: 'Challenge timing', value: 'Use the request deadline height; block counts are not fixed seconds' },
      { name: '51025 request cost', value: 'At least 0.01 FB prepaid witness fee, a separate 10 FB submitter bond, and the transaction runtime fee' },
    ],
    sections: [
      {
        heading: 'Check these details on every chain',
        bullets: [
          'Connect the DEX wallet, select asset and source network, and verify the DEX recipient field. Leaving it empty uses the current wallet fbc1… address.',
          'Distinguish native coins from contract tokens. Source chain, contract address, and DEX asset ID identify a token; matching names do not combine balances.',
          'Keep external network fees in the external wallet. On 51025, a manual DEX credit request also needs an FB runtime fee, at least 0.01 FB prepaid witness fee, and another 10 FB of available balance for the submitter bond.',
        ],
      },
      {
        heading: 'BTC: obtain the current address before paying',
        bullets: [
          'Select BTC, enter the DEX recipient, and click “Get Deposit Address”. Send to the complete BTC address returned for this request, not the displayed Vault public key or an old address.',
          'If the page confirms a registered watch, review its expiry and allow confirmation time so the transaction is mined before expiry. The operator verifies the UTXO, submits the DEX credit request, pays its fees, and locks its bond automatically. The submitter reward belongs to the operator, not the BTC payer.',
          'If automatic watching is unavailable, keep the txid, vout, and deposited amount, fill them in below, and click “Submit Deposit Request” to sign with the DEX wallet.',
          'For example, after paying exactly 0.010 BTC to the displayed address, save the txid and identify the vout containing that output. Change in another output is not the deposit.',
          'Do not pay again to trigger detection. Keep the original address, txid, vout, and watch-expiry information, then inspect the record or ask the operator to investigate.',
        ],
      },
      {
        heading: 'Account chains: use the contract deposit action',
        bullets: [
          'For Ethereum, BNB Chain, Base, Arbitrum, or TRON, select source network and asset. For a token outside the catalog, choose Contract Address and enter the exact contract, then click “Prepare safe deposit”.',
          'Enter the quantity and select the external wallet. For contract tokens, connect and identify the token first; verify symbol, decimals, and raw amount. Restore metadata reads and review changes rather than guessing 8 or 18 decimals.',
          'Click the external-wallet deposit action. If token allowance is insufficient, the wallet first requests approval for this amount, then confirms the contract deposit. Native coins need no token approval. Check network, Vault, DEX recipient, amount, and fees at each prompt.',
          'Do not make an ordinary transfer to an account-chain Vault: it does not record the DEX recipient and cannot replace the contract deposit. Token approval alone also does not deposit funds.',
          'Save the external hash and wait for the deposit event. Use “Read deposit event” if needed. Once the page says the proof is ready, submit the deposit request below and approve the DEX transaction.',
        ],
      },
      {
        heading: 'Witnessing, challenge window, and credit',
        bullets: [
          'A successful request typically progresses through witness selection, voting, and the challenge window. These stages do not yet mean the balance is available.',
          'After the challenge window with no active valid dispute, the request reaches RECHARGE_FINALIZED. If challenged, inspect arbitration and use the final decision to determine credit or rejection.',
          'On 51025, final PASS returns the submitter’s 10 FB bond. It allocates two floor(gross / 10,000) shares in the deposited asset: one to the submitter and one shared by eligible PASS witnesses. The receiver gets gross minus both shares. Final FAIL forfeits the bond and pays no successful-deposit reward. The prepaid witness fee settles separately from gross.',
          'Use Bridge → History and Recharge Witnessing to check request ID, gross external amount, state, and deadline height, then verify the net credit in Account Center.',
        ],
      },
      {
        heading: 'External success without DEX credit',
        bullets: [
          'Check external execution and confirmations, then the BTC address/vout or account-chain event recipient, contract, and amount. Successful approval is not a successful deposit.',
          'For an account-chain deposit, check DEX credit status first. If the previous DEX request is confirmed failed or rejected and the page allows retry, reuse the original deposit proof. This resubmits the claim on the original payment and does not require another external transfer.',
          'If the DEX request is pending, its result is unknown, configuration changed, or an old local record cannot be restored, retain the original evidence and hash before further action.',
          'Saving the record and starting another deposit starts a separate payment; it does not cancel or complete the first. A credited deposit proof cannot be used for a second credit.',
        ],
      },
    ],
  },
  {
    id: 'withdraw-flow',
    title: 'Withdrawal Flow',
    summary: 'Request with your DEX wallet and wait for SIGNED, then complete the account-chain claim or BTC broadcast. A successful DEX request deducts funds; external delivery requires a separate check.',
    params: [
      { name: 'Entry', value: 'Bridge → Withdraw; resume an existing withdrawal from History' },
      { name: 'Keep', value: 'DEX request transaction ID, withdrawal ID, and external hash' },
      { name: 'Account-chain fees', value: 'The claim wallet pays external execution fees; the recipient is fixed' },
      { name: 'BTC fees', value: 'Deducted from the requested amount; the broadcaster does not pay that miner fee again' },
      { name: 'Account-chain execution order', value: 'The vault executes batches in sequence for their key epochs. The external contract must finish pending old batches before switching keys' },
    ],
    sections: [
      {
        heading: 'Check before requesting',
        bullets: [
          'Expand the asset selector to use My Assets or search by name or address. Verify source network and contract; matching names do not make assets interchangeable across chains.',
          'For contract tokens, identify decimals through the corresponding external wallet when prompted. Check available quantity, decimal places, fees, and FB runtime balance; never guess decimals after a read error.',
          'Enter the external recipient on the target network. A successful request fixes this address; the claim step cannot change it. Verify the full address before signing in the DEX wallet.',
          'For account chains, prepare the external currency or resources needed to claim. The signatures bind the original request, vault, key epoch, and batch sequence. Open that request when claiming.',
        ],
      },
      {
        heading: 'Step 1: DEX request and signing',
        bullets: [
          'Wait for final execution of the DEX request. Success deducts the requested amount and creates the withdrawal record. Keep both its DEX transaction ID and withdrawal ID.',
          'QUEUED means waiting. If the request needs an active Vault, balance, or BTC UTXOs, inspect the withdrawal monitor for the reason and progress.',
          'After Claim reserves funds, signing begins. SIGNED unlocks step two and means the signature is available, not that the external transaction has been broadcast or delivered.',
          'Return through Bridge → History → Continue Withdrawal, or use Resume a Withdrawal and select the matching ID type before entering a DEX request transaction ID or withdrawal ID.',
        ],
      },
      {
        heading: 'Step 2: claim on an account chain',
        bullets: [
          'For Ethereum, BNB Chain, Base, Arbitrum, or TRON, check the fixed recipient, asset, amount, Vault, and signing epoch, then connect a supported wallet for that network.',
          'The payer can differ from the recipient but must have sufficient execution fees on the correct network. EVM transactions use the network native currency; TRON needs resources or TRX, with a suitable feeLimit.',
          'Click “Pay fees and claim in wallet”, review network and fees, and approve. A package carrying a Vault handover can cost more to execute; use the current wallet estimate.',
          'A committee handover can finalize on DEX before it executes externally. The external account-chain vault applies the handover only after pending signed batches from the old epoch execute in sequence. If claiming reports a pending old batch or a sequence mismatch, keep the original withdrawal ID and error and follow that request.',
          'After a hash is returned, wait for the external-execution check to confirm the Vault executed, and inspect the external explorer. A locally saved hash or wallet submission message alone is not delivery.',
          'After an external failure, check its hash, execution status, and signature validity. Correct network or fee issues and retry the claim on the original withdrawal. Do not create another DEX withdrawal to retry claiming.',
        ],
      },
      {
        heading: 'Step 2: obtain or broadcast the BTC transaction',
        bullets: [
          'Networks with the BTC broadcast service enabled attempt automatic broadcast. You can also use the supported OKX Wallet action or download the transaction and dependencies for a BTC broadcast tool.',
          'Broadcast parent dependencies in the package order before the withdrawal transaction; parents already present need not be broadcast again. Use the complete raw transaction, not internal signature debugging fields.',
          'For a requested debit of 0.005 BTC, the recipient gets that amount minus the assigned miner fee shown in the result. The broadcaster need not fund that same miner fee from another wallet.',
          'A txid or tool acceptance is not confirmation. Verify transaction contents and confirmations on BTC. Continue using the same txid after broadcast instead of creating a second DEX withdrawal.',
        ],
      },
      {
        heading: 'When cancellation is possible',
        bullets: [
          'Only the original requester can cancel while the request is QUEUED and before FROST Claim starts. The page or History shows Cancel Withdrawal when applicable.',
          'Cancellation is a new DEX transaction with a runtime fee. If Claim executes first, cancellation fails and does not stop signing.',
          'The requested amount returns to available DEX balance only after successful cancellation reaches CANCELLED. Prior runtime fees are not refunded by this action. SIGNING or SIGNED requests cannot be cancelled to recover the amount.',
        ],
      },
      {
        heading: 'Use the original record during recovery',
        bullets: [
          'If the DEX request definitively failed without deducting the withdrawal amount, fix the issue and request again. If a rejected prompt or timeout leaves submission uncertain, check the transaction first.',
          'Keep using the same withdrawal ID once a valid record exists. For stalled queueing or signing, give the operator the IDs, current stage, and error instead of creating another debit.',
          'If the claim has executed, stop there. If the RPC is unavailable, keep checking. When you see a key-rotation warning, check the original withdrawal and execution status on the external vault before taking further action.',
        ],
      },
    ],
  },
  {
    id: 'vault-signing',
    title: 'Vaults and Distributed Signing',
    summary: 'A vault is an address or contract that holds cross-chain assets. Withdrawals and authority handovers require both a threshold signature from the operator committee and an independent signature from the fixed Guardian.',
    params: [
      { name: 'Vault definition', value: 'A protocol address or contract that holds real external assets backing cross-chain DEX balances' },
      { name: 'Control model', value: 'The operator committee reaches its signing threshold and the fixed Guardian authorizes the same request independently' },
      { name: 'Committee source', value: 'Regular reviews select the Top K Active miners meeting maturity and stake-lock requirements' },
      { name: 'Term review', value: '120,960 finalized blocks, about seven days at five seconds per block. Retained fixed-key genesis mode skips automatic rotation' },
      { name: 'User-visible', value: 'Vault, epoch, signing stage, request status, and external payload' },
      { name: 'Miner-visible', value: 'DKG sessions, signing jobs, participant state, vault balance, and errors' },
      { name: 'Committee / Threshold', value: 'K = 4 / 8 / 20 / 32 / 64 / 100; t = 3K/4 = 3 / 6 / 15 / 24 / 48 / 75' },
    ],
    sections: [
      {
        heading: 'What is a vault?',
        bullets: [
          'BTC vaults use a Taproot script to verify the operator and Guardian signatures. The vault contracts on Ethereum, BNB Chain, Base, Arbitrum, and TRON verify each signature separately.',
          'Real assets from deposits enter a vault. The DEX records the corresponding cross-chain balance only after deposit finalization, and later pays withdrawals from an eligible vault.',
          'A vault is neither a user wallet nor an account that a node operator may spend freely. Committee members may sign only confirmed protocol requests.',
          'A vault is identified by its network, Vault ID, and Key Epoch. Available balances are tracked per asset, and normal service uses an ACTIVE vault.',
        ],
      },
      {
        heading: 'Operator committee and Guardian responsibilities',
        body: 'Operator committee members each hold one key share and combine enough shares to produce the operator signature. The fixed Guardian independently verifies and signs the same request with a separate key. Withdrawals and handovers require both authorizations. See Protocol → Vault DKG for both public keys.',
      },
      {
        heading: 'How the committee is formed',
        bullets: [
          'A candidate must be Active and must have remained Active for the vault-maturity period. Probation miners are probed for availability only: they do not vote and cannot enter the vault candidate pool.',
          'Candidates are ranked by mature stake in descending order. Ties go first to the miner that became Active earlier, then to the lower miner index. The first K are selected. Every stake top-up completes its own maturity period before it affects this ranking.',
          'Only K = 4 / 8 / 20 / 32 / 64 / 100 are valid committee sizes. Their fixed thresholds are t = 3K/4: 3 / 6 / 15 / 24 / 48 / 75. The threshold does not fall when fewer participants are available.',
          'Selection alone does not activate the committee. Members must complete DKG commitments, share exchange, dispute handling, and group-public-key validation. If the fixed K / t cannot be completed, that DKG attempt fails and retries with replacements; it does not shrink the committee or lower the threshold.',
          'After validation, the membership, group public key, and Key Epoch are written into vault state. The vault performs normal signing only after it becomes ACTIVE.',
        ],
      },
      {
        heading: 'DKG and FROST signing',
        bullets: [
          'DKG lets the committee jointly create a group public key and individual key shares without ever creating or storing one complete private key.',
          'A withdrawal template is first validated and reserved by an on-chain Claim. Every participant independently rebuilds and checks the network, asset, amount, recipient, vault, epoch, and template hash.',
          'FROST combines enough valid shares into the operator signature. SIGNED means the DEX verified and recorded both authorizations. Check external broadcast, execution, and confirmation separately.',
          'A committee rotation creates a new Key Epoch. An old vault may become DRAINING or DEPRECATED and hands service to the new vault after asset migration.',
        ],
      },
      {
        heading: 'How fixed terms are reviewed',
        bullets: [
          'In production, a larger tier activates only when the number of eligible mature candidates reaches 100 × K: 800 for K=8, 2,000 for K=20, 3,200 for K=32, 6,400 for K=64, and 10,000 for K=100. The genesis vault starts at K=4.',
          'The first review is due 120,960 blocks after the committee actually activates. Changes in headcount, ranking, or availability are evaluated at term end. Elapsed time depends on block production.',
          'If the member set is unchanged at review, the term renews with the same key and Key Epoch even if the ranking changed. If too few eligible candidates remain, the current committee and threshold stay in place and review continues at later heights. An active K tier never shrinks automatically.',
          'When enough eligible candidates form a different committee at term end, the new members run DKG. The old operator group and the fixed Guardian then authorize the handover. The old committee retains its signing capability until the relevant handover is complete.',
          'Networks using retained fixed-key genesis mode keep the fixed committee and skip automatic rotation.',
        ],
      },
      {
        heading: 'Normal waiting and abnormal signals',
        bullets: [
          'A short wait is normal because enough participants must be online. External congestion, maintenance, and vault rotation can extend processing time.',
          'For one delayed withdrawal, determine whether it is queued, waiting for vault balance, waiting for committee signatures, or already has an external transaction.',
          'If many withdrawals remain in signing for a long time, operators should inspect committee availability, threshold progress, vault balance, external-chain health, and migration state.',
          'Do not bypass template verification, lower the threshold, or resubmit a withdrawal merely because it is waiting; those actions increase custody and duplicate-debit risk.',
        ],
      },
    ],
  },
  {
    id: 'cross-chain-status',
    title: 'Track Cross-chain Progress',
    summary: 'Check the DEX transaction result, cross-chain processing stage, and external execution separately. Interpret “success” in the context of the stage showing it.',
    params: [
      { name: 'Deposits', value: 'Bridge → History; Recharge Witnessing → request progress' },
      { name: 'Withdrawals', value: 'Bridge → History / Resume a Withdrawal; withdrawal monitor' },
      { name: 'External result', value: 'Read account-chain Vault execution, or inspect BTC txid and confirmations' },
    ],
    sections: [
      {
        heading: 'Read the stage before acting',
        body: '| Display | Meaning | Next step |\n| --- | --- | --- |\n| External deposit complete / event found | Payment or evidence is available; DEX credit is not yet guaranteed | Confirm a witness request exists and inspect it |\n| Selection / voting / challenge window | Deposit verification continues | Check deadline height and disputes |\n| RECHARGE_FINALIZED | Final DEX credit | Reconcile account and asset activity |\n| QUEUED / SIGNING | DEX withdrawal queue or signing | Check Vault, planning, or signing progress |\n| SIGNED | A signed withdrawal result exists | Claim on the account chain; check BTC broadcast and confirmations |\n| External unchecked / RPC unavailable | Execution evidence is unavailable | Connect the correct wallet and retain the original hash |\n| External unclaimed / pending | Completion has not been observed | Check whether another device submitted it before retrying |\n| Vault confirms successful execution | The contract confirms execution of this request | Inspect the external record and fixed recipient |\n| CANCELLED | DEX cancellation succeeded | Check the refund; do not claim |',
      },
      {
        heading: 'Recover the original record',
        bullets: [
          'For deposits, keep network, external hash, recipient, asset, and amount. Also keep BTC vout or the account-chain deposit event and DEX request ID.',
          'For withdrawals, keep both the DEX request transaction ID and withdrawal ID. Select the matching ID type when resuming; an external TxHash is not a DEX request ID.',
          'Local history may be incomplete after changing device, browser, or node. Query the original ID. Wallet feedback without chain evidence does not establish delivery.',
        ],
      },
      {
        heading: 'Find original requests and BTC outputs by Vault',
        bullets: [
          'In Protocol → Vault DKG, select the network and Vault, then expand Vault Requests. Choose View details under Witness Requests or Withdraw Requests to open the original DEX request in a new tab with the selected miner node.',
          'For a BTC Vault, DEX registered UTXOs shows custody addresses, amounts, sources, and reservation status. Expand Outpoint for txid:vout. Copy an address or open it in mempool.space where the network supports a link.',
          'The table shows registered outputs that the DEX has not marked spent. Check the external chain for current BTC confirmations. For a new deposit, get an address for the DEX recipient from Bridge → Deposit.',
        ],
      },
      {
        heading: 'What to provide for investigation',
        body: 'Provide network, DEX account, asset ID or contract, amount, time, request ID, external hash, current stage, and full error. For deposits, state whether you used automatic watching, manual reporting, or an account-chain contract deposit. Never send private keys, seed phrases, or wallet passwords.',
      },
    ],
  },
  {
    id: 'explorer-backend',
    title: 'Install and Run the Explorer Backend',
    summary: 'Run the Explorer backend and website on Linux, connect to an existing Frostbit node, and check index progress.',
    sections: [
      {
        heading: 'Use the release bundle',
        body: 'Download `dex-<version>-linux-amd64.zip` from [Frostbit Releases](https://github.com/commonstate-labs/frostbit/releases/latest), extract it, and enter `dex/`. The backend executable is `explorer` and the website is in `explorer_web/dist/`. Prepare the public runtime configuration for the target network, including `config.json` and `genesis.json`, and select a trusted node that is running and reachable.',
      },
      {
        heading: 'Start locally',
        body: 'Set `CONFIG_DIR` to the network configuration directory and `NODE` to your node address. `--nodes` takes comma-separated `host:port` addresses. Choose one of them for `--sync-node`. Set `--data-dir` to a separate Explorer index directory.',
        codeLabel: 'Bash',
        code: `CONFIG_DIR=/path/to/network-config
NODE=127.0.0.1:6000
./explorer \\
  --config-dir="$CONFIG_DIR" --listen=127.0.0.1:8080 \\
  --nodes="$NODE" --sync-node="$NODE" \\
  --web-dir=./explorer_web/dist --data-dir=./explorer_data \\
  --ip-tls=false --admin-listen=""`,
      },
      {
        heading: 'Check the service and index',
        body: 'Open [http://127.0.0.1:8080](http://127.0.0.1:8080) in a browser. Check the node list at `/api/nodes` and compare the sync status and `sync_height` at `/api/sync/status` with the target node height. The website can become available while the index is still catching up.',
        codeLabel: 'Bash',
        code: `curl -fsS http://127.0.0.1:8080/api/nodes
curl -fsS http://127.0.0.1:8080/api/sync/status`,
      },
      {
        heading: 'Build from source',
        body: 'Download `frostbit-<version>-source.zip` from the same release and enter its source root. Use Go 1.24 and Node.js 22.12 or a later 22.x version. After building, run the same start command with `./frostbit-explorer` as the executable and `./explorer/dist` as `--web-dir`.',
        codeLabel: 'Bash',
        code: `npm --prefix explorer ci
npm --prefix explorer run build
go build -mod=readonly -trimpath -o ./frostbit-explorer ./cmd/explorer`,
      },
    ],
  },
  {
    id: 'search-records',
    title: 'Search Transactions, Blocks, and Accounts',
    summary: 'Use global search and detail pages to inspect authoritative transaction, block, and account information.',
    params: [
      { name: 'Transaction search', value: 'Use transaction ID to inspect execution status, receipt, fee, error, and block height' },
      { name: 'Account search', value: 'Use fbc1 address to inspect balances, nonce, orders, positions, stake, and activity' },
      { name: 'Block search', value: 'Use height or block ID to inspect ordered transactions, proposer, and finality' },
    ],
    sections: [
      { heading: 'Search guidance', bullets: ['Distinguish pending submission, failed execution, and finalized success.', 'Use transaction ID, account, pair, status, and block height together when narrowing an issue.', 'A transaction hash alone does not prove successful execution; the final receipt is authoritative.'] },
      {
        heading: 'Switch Explorer backends',
        bullets: [
          'Open the globe menu in the upper right, enter the full HTTPS Explorer address, such as https://server1.frostbit.io, and select + to add it and switch. The default on frostbit.io is Current Explorer (same-origin), served by SSD.',
          'For a public Explorer with direct IP access enabled, use https://PUBLIC_IP:8445. Omit credentials, query parameters, and fragments. Use the Explorer HTTPS API address here and select the miner node separately.',
          'Switching reloads queries and live subscriptions. Check the selected miner node, finalized height, and original transaction or request ID. Select Current Explorer (same-origin) to return to the default.',
        ],
      },
    ],
  },
  {
    id: 'node-health',
    title: 'Node Health',
    summary: 'Node health combines connectivity, finalized height, synchronization state, block activity, and protocol worker status.',
    params: [
      { name: 'Healthy state', value: 'Height advances, nodes agree, and latency remains stable' },
      { name: 'Minimum mining stake', value: '32 FB by default' },
      { name: 'Minimum witness stake', value: '32 FB by default' },
      { name: 'Exit lock', value: '50 blocks by default for witnesses and 100 blocks for node miners. Case duties or vault handover can extend the wait.' },
      { name: 'Risk state', value: 'Stalled height, state divergence, latency spikes, or cross-chain backlog' },
      { name: 'Priority', value: 'Determine whether the problem is local or network-wide before changing state' },
    ],
    sections: [
      { heading: 'Routine observation', bullets: ['Finalized height advances near the network frontier.', 'Peers remain connected and consensus is not paused for catch-up.', 'No persistent errors appear in cross-chain, storage, or finalization workers.', 'Compare the local node with several trusted peers; one remote height is only a lag hint.'] },
    ],
  },
  {
    id: 'activity-history',
    title: 'Activity and History',
    summary: 'Review account actions, completed orders, trades, positions, fees, transfers, and archived protocol records.',
    params: [
      { name: 'User purpose', value: 'Verify transfers, orders, fills, positions, fees, rewards, deposits, and withdrawals' },
      { name: 'Support purpose', value: 'Reconstruct an issue from account, transaction ID, status, and height' },
      { name: 'Miner purpose', value: 'Correlate user-visible outcomes with node logs and protocol state' },
    ],
    sections: [
      { heading: 'Troubleshooting basis', body: 'Open orders and positions remain active state; completed records move to query or archive history. Use transaction ID, account, pair, status, and height together, and remember historical data may be pruned under configured disk pressure.' },
    ],
  },
  {
    id: 'witness-mining-overview',
    title: 'Witness Mining Overview',
    summary: 'Witness miners independently verify cross-chain deposit facts and submit on-chain votes. They do not produce blocks, and witness participation does not grant control of a vault.',
    params: [
      { name: 'Core duty', value: 'Verify external-chain transactions, vote on time, and participate in challenges and arbitration' },
      { name: 'Minimum stake', value: 'Currently 32 FB by default; the live Witness Mining page takes precedence' },
      { name: 'Requirements', value: 'Reliable availability, independent external-chain verification, and protected signing keys' },
      { name: 'Reward source', value: 'Rewards for correctly and timely completing valid witness tasks' },
      { name: 'Main risk', value: 'Wrong votes may forfeit case collateral, and unfinished duties delay withdrawal. Silence or being offline is not itself fined' },
    ],
    sections: [
      { heading: 'Different from node mining', bullets: ['Witness miners verify cross-chain deposits; node miners validate transactions, participate in consensus, and produce blocks.', 'The roles have separate stake and status. New ordinary nodes register separately; genesis miners can submit witness registration automatically, but still need a successful finalized receipt.', 'One operator may perform both roles. Check duties, staked balances, monitoring, and recovery separately; simply running a node does not replace external verification.'] },
      { heading: 'Who should participate', body: 'This role fits operators who can remain available, understand external-chain confirmation rules, independently verify evidence, and accept responsibility for each vote.' },
      { heading: 'Hardware and external data', body: 'When running a DEX node too, follow Recommended Hardware and Capacity. Witness work also requires available, synchronized external RPCs. Self-hosted Bitcoin, EVM, and TRON nodes require additional CPU, RAM, SSD capacity, and synchronization space; the DEX 1.5 TB requirement does not cover all those chains.' },
    ],
  },
  {
    id: 'witness-mining-join-exit',
    title: 'Join, Stake, and Exit',
    summary: 'Register and stake through Witness Miner. To exit, submit a request, finish existing case duties, then claim the remaining stake in a separate transaction.',
    params: [
      { name: 'Entry point', value: 'Protocol → Witness Miner' },
      { name: 'Minimum total stake', value: 'Currently 32 FB by default' },
      { name: 'Exit', value: 'Request exit, wait for the lock and case duties to end, then select Claim Stake' },
      { name: 'Unstake lock', value: '50 blocks by default, counted from the exit request’s execution height. Use the unlock height shown on the page.' },
      { name: 'Return destination', value: 'Available FB balance of the original witness staking address' },
    ],
    sections: [
      { heading: 'Register and stake', bullets: ['Use a long-lived fbc1 account and reserve enough FB for transaction fees.', 'Verify that external-chain data sources, networking, time, and alerting can operate continuously.', 'In Protocol → Witness Miner, enter the stake amount and select **Register & Stake**. Keep enough available FB after staking.', 'Check the finalized registration receipt, witness status, and staked amount. Registration makes the account eligible for selection when it meets the protocol requirements.'] },
      {
        heading: '1. Request exit',
        bullets: [
          'Open Protocol → Witness Miner and connect the original fbc1 staking wallet on the same network. Keep enough available FB to pay for both the exit request and the claim.',
          'In the active witness card, select **Request exit** and confirm the wallet signature. Save the TxID and check the transaction details for finalized success.',
          'Once the request succeeds, the status changes to `UNSTAKING` and new case assignments stop. Record the execution height as the starting point for the lock.',
        ],
      },
      {
        heading: '2. Wait for the lock and close existing cases',
        bullets: [
          'Compare **Current Height**, **Unlock Height**, and the blocks left. For example, a request executed at height 1,000 with the default 50-block lock reaches its earliest claim height at 1,050. Follow the block height when the time estimate differs.',
          'Keep fulfilling assigned deposit witness, ordinary arbitration, and final-court duties. Check that both **Open cases** and **Reserved for cases** show zero. If a field shows `--`, refresh the state on a synchronized node before assuming the duty has cleared.',
          'A case can remain open after you vote or after it disappears from Pending Requests. A final-court seat can also keep a case open with zero reserved stake. Follow the case through final settlement.',
        ],
      },
      {
        heading: '3. Claim the remaining stake',
        bullets: [
          'When the lock has elapsed and both case counters are zero, select **Claim Stake** with the same wallet. Confirm the second transaction and check its own TxID for finalized success.',
          'Check that witness-locked FB drops to zero and the remaining stake appears in the original address’s available FB balance. The on-chain witness state becomes `EXITED` and the panel returns to the registration form.',
          'Case rewards credit automatically when the case settles. Claim Stake returns only the principal left after penalties. If penalties reduce the stake to zero, the claim fails with `nothing to claim`.',
        ],
      },
      {
        heading: 'When an action is unavailable',
        bullets: [
          'If the claim is disabled, check the unlock height and both case counters. Repeating the exit request cannot advance either condition.',
          'If a transaction is pending or failed, look up its TxID before retrying. Check the receipt, selected network, connected staking address, and available FB for fees.',
        ],
      },
    ],
  },
  {
    id: 'witness-mining-workflow',
    title: 'Witness Tasks and Voting',
    summary: 'A witness task moves through request creation, witness selection, independent verification, on-chain voting, public challenge, and possible arbitration.',
    params: [
      { name: 'Verify', value: 'Transaction hash, asset, amount, destination, confirmations, and uniqueness' },
      { name: 'Voting rule', value: 'Vote only from independently verified evidence' },
      { name: 'Deadline rule', value: 'Finish within the voting window; never guess when evidence is unavailable' },
    ],
    sections: [
      { heading: 'Standard workflow', bullets: ['Record request ID, chain, asset, and external transaction identifier when selected.', 'Use a trusted external-chain node to verify existence, confirmation, amount, and destination.', 'Check that the same external transaction or output has not already been consumed.', 'Submit PASS, FAIL, or a supported abstention result, then confirm the vote finalized successfully.', 'Watch the challenge period and independently recheck evidence if selected for arbitration.'] },
      { heading: 'When verification is unavailable', body: 'If an external node is behind, sources disagree, or evidence is insufficient, repair the source or abstain as the protocol allows. A submitted vote is not a successful vote until its final receipt confirms execution.' },
    ],
  },
  {
    id: 'witness-mining-rewards-risk',
    title: 'Rewards, Challenges, and Slashing',
    summary: 'Understand prepaid witness rewards, per-case collateral, three expanding ordinary arbitration rounds, and a fixed four-member final court. Timeouts cause another voting round, not a default verdict.',
    params: [
      { name: 'Witness fee', value: 'Collected into escrow before work; eligible rewards are paid automatically at final settlement' },
      { name: 'Case collateral', value: '10 FB per original witness per case; at least 10 FB per ordinary arbitrator per case' },
      { name: 'Challenge threshold', value: 'max(10 FB, 10% of this case’s original-witness collateral snapshot)' },
      { name: 'Ordinary arbitration', value: 'Three expanding rounds; current configurable implementation defaults are 4/8/16 members and 75%, not confirmed final protocol parameters' },
      { name: 'Round deadline', value: '10,000 blocks for each ordinary or final-court round' },
      { name: 'Final court', value: 'Four fixed members; three matching votes in the same round are required' },
      { name: 'Vote meaning', value: 'PASS means the deposit evidence is valid; FAIL means it is invalid' },
    ],
    sections: [
      { heading: 'Rewards' },
      { heading: 'Prepaid fees and automatic payment', level: 3, bullets: [
        'The requester actually pays the witness fee before work starts. It remains in escrow until final settlement; a declared fee amount cannot create rewards without payment.',
        'Eligible original witnesses, ordinary arbitrators, and final-court members whose effective votes match the final result share the witness fee equally. Each address receives one fee share across roles and rounds.',
        'A wrong frozen original or ordinary position disqualifies the address from all case rewards, even if a later role voted correctly. An address serving only on the final court earns a fee share only through a matching vote in the deciding court round.',
        'Rewards are transferred automatically to balances after final settlement. There is no separate Claim Reward step. A fee with no eligible recipient is burned.',
        'When a deposit finishes without arbitration, settlement distributes its prepaid witness fee and releases this case’s duties and reservations; it does not slash witnesses.',
      ] },
      { heading: 'Challenges' },
      { heading: 'Funding and admission', level: 3, bullets: [
        'Contribute only while the deposit remains in its public challenge period and the case is still fundraising. An admitted case cannot accept more contributions, and a finalized deposit cannot be challenged again.',
        'Find the request in its public challenge window under Deposit Witness, connect the DEX wallet, and review the contribution amount. The challenge button refreshes the quote and submits a contribution with a built-in reason; it has no custom reason or evidence-attachment field. Check the debit before signing and retain verifiable external evidence separately.',
        'The threshold is max(10 FB, 10% of this case’s original witness reserves), using the frozen original-stage snapshot rather than later registration stake changes. Follow the original request to verify the contribution transaction and challenge state.',
        'The first contribution creates CHALLENGE_FUNDING. Each new contribution must be at least 10 FB and is actually deducted from available FB into the same case; multiple addresses may contribute.',
        'Once total contributions reach the threshold, the deposit becomes RECHARGE_CHALLENGED and the challenge becomes CHALLENGE_PENDING_SELECTION, waiting for its future-block selection beacon.',
        'If the public period ends before funding reaches the threshold, every contributor automatically receives the exact principal back. Ordinary arbitration and final-court retries do not collect that principal again.',
        'Include reproducible evidence: the external transaction, block height, output index where applicable, asset, destination, amount, and confirmations. Confirm the finalized receipt and case state after submission.',
      ] },
      { heading: 'Challenge states', level: 3,
        body: `| State | Meaning | What to check |
| --- | --- | --- |
| RECHARGE_CHALLENGE_PERIOD | The original result is public and funding may still be open | Publicity deadline and contribution quote |
| CHALLENGE_FUNDING | Contributions have not yet activated arbitration | Actual total, required amount, and automatic refund after an underfunded deadline |
| CHALLENGE_PENDING_SELECTION | The admitted case is waiting for selection or a scheduled selection retry | Selection height and qualified candidates; this is not a verdict |
| CHALLENGE_ACTIVE | An ordinary or final-court voting round is open | Stage, fixed round identity, members, required votes, and deadline |
| CHALLENGE_FINALIZED | The challenge has finished, including an underfunded closure | Deposit status, final votes where present, principal refunds, rewards, and burns |`,
      },
      { heading: 'Ordinary arbitration: three rounds', level: 3, bullets: [
        'Ordinary arbitration has three rounds with increasing planned committee sizes. Each round lasts 10,000 blocks and may finish early when the required number of votes agrees.',
        'The current implementation defaults are 4, 8, and 16 planned members with a 75% threshold. These values are configurable implementation defaults, not confirmed final protocol parameters.',
        'The voting threshold uses the planned committee size. Fewer selected or online participants do not reduce it. Votes from different rounds never combine to reach a threshold.',
        'At the start of the third ordinary round, freeze the four highest-staked eligible ACTIVE registered witnesses as this case’s final court. Rank by total registration stake, with address order breaking ties. If fewer than four qualify, wait for a scheduled selection retry.',
        'If the third ordinary round expires without a result, freeze the ordinary-stage positions and enter the final court.',
      ] },
      { heading: 'Fixed final court and repeated voting', level: 3, bullets: [
        'The case retains the same four court members after selection. Later stake rankings, exit requests, or offline periods do not replace them.',
        'Three members must vote the same direction in the same round to decide. With fewer than three matching votes, the 10,000-block deadline clears current-round ballots and starts another round with the same members.',
        'There is no retry limit, replacement of inactive judges, terminal “unable to decide” result, lowered threshold, or automatic win or loss caused by timeout. A case can wait indefinitely for its fixed court.',
        'Final-court members post no additional case bond for that role. Existing original-witness or ordinary-arbitrator obligations still apply when the same address served those roles.',
        'Being offline, abstaining, or casting a minority final-court vote does not itself cause a penalty. Only the deciding court round supplies court fee votes.',
      ] },
      { heading: 'PASS, FAIL, and challenge success', level: 3, bullets: [
        'PASS means the external deposit evidence is valid; FAIL means it is invalid. These directions do not mean “uphold” and “overturn” the original result.',
        'The challenge succeeds when the final evidence direction differs from the original result. Final PASS permits deposit finalization; final FAIL rejects the deposit.',
        'ABSTAIN counts toward neither direction. It consumes that address’s ballot for the current round but does not erase its last valid ordinary-stage direction.',
        'Sign the current case and ballot-round identity. Old-round, duplicate, late, and non-member ballots cannot contribute to the decision.',
      ] },
      { heading: 'Collateral and responsibility' },
      { heading: 'One protected reservation per case and address', level: 3, bullets: [
        'Each original witness reserves 10 FB for the case. Each ordinary arbitrator guarantees at least 10 FB. If an address has multiple bonded roles, reserve the largest required amount and only top up the difference.',
        'Repeated rounds do not create duplicate collateral. Reservations across cases cannot exceed real locked stake, and one case cannot consume collateral reserved for another.',
        'Original-stage positions are frozen independently. During ordinary arbitration, a later valid directional vote may correct an earlier ordinary vote; the last valid direction freezes when that stage ends.',
        'A frozen original or ordinary position opposite to the final result forfeits the address’s case collateral once and excludes it from all case rewards. Later correct court voting cannot erase that responsibility.',
        'Silence and continued offline periods are not fined. Remaining case collateral is released from its reservation at closure; registration stake still follows the safe-exit requirements.',
      ] },
      { heading: 'Distribution when the challenge succeeds', level: 3, bullets: [
        'Refund each contributor’s actual principal first. Principal refunds are separate from reward eligibility, including for a contributor who is a court member or has a wrong frozen position.',
        'Of the collateral actually slashed, burn 10%. Allocate 45% to eligible challenge contributors in proportion to their contributions, and 45% equally among eligible correct original witnesses and ordinary arbitrators.',
        'If either reward group has no eligible recipient, burn that group’s allocation. Do not give it to the other group.',
        'A qualifying non-court address that both contributed and voted correctly may receive payments from both groups. Each address is counted once within each group.',
        'Every final-court address is excluded from both slash-reward groups, even if it also contributed or served an earlier role. Court members may still receive eligible prepaid witness-fee shares.',
      ] },
      { heading: 'Distribution when the challenge fails', level: 3, bullets: [
        'Pool the failed challenge’s actual principal with any collateral actually slashed in that case. Divide the entire pool equally among eligible correct original witnesses and ordinary arbitrators on the winning side.',
        'Final-court addresses cannot receive any part of this pool, even through another role. If no eligible non-court recipient exists, burn the pool.',
        'The failed challenge principal is not refunded. Prepaid witness fees are settled separately under the fee-eligibility rules.',
      ] },
      { heading: 'Exact amounts and monitoring', level: 3, bullets: [
        'All amounts use integer minimum units. Proportional and equal allocations use deterministic remainder handling, so recipients may differ by one minimum unit and every collected unit is accounted for.',
        'Payments and burns never exceed funds actually collected or slashed. Case reservations are not an extra source of money.',
        'For a waiting case, check the current stage, round identity, deadline, member list, and finalized vote receipts. Waiting or repeated voting is not a reason to submit another challenge.',
        'After closure, verify principal refunds, fee payments, each reward pool, actual collateral debits, burns, and released duties against finalized records and account balances.',
      ] },
    ],
  },
  {
    id: 'witness-mining-operations',
    title: 'Daily Operations and Troubleshooting',
    summary: 'Witness operations should prevent missed selections, stale external-chain data, unverified votes, and loss of evidence during a challenge.',
    params: [
      { name: 'Monitor', value: 'Registration, pending tasks, vote receipts, external-chain sync, and challenge alerts' },
      { name: 'Alert on', value: 'Approaching deadlines, failed votes, stalled external heights, or abnormal selection behavior' },
      { name: 'Preserve', value: 'Request ID, external transaction, verification height/source, vote txID, and timestamps' },
    ],
    sections: [
      { heading: 'Checklist', bullets: ['Registration and stake remain valid, with enough available FB for fees.', 'External-chain nodes are synchronized and independent sources agree on confirmations.', 'Pending, voted, and challenged tasks continue to update; check automatic rewards, refunds, and withdrawable exit stake separately.', 'Vote transactions finalize without nonce, signature, balance, or timeout errors.'] },
      { heading: 'Failure order', body: 'Pause automated voting, preserve task and external evidence, then isolate local networking, external data, wallet signing, or on-chain execution. Resume only after read-only verification agrees; never backfill expired tasks or bypass verification.' },
      { heading: 'Symptoms and next steps', body: `| Symptom | Check first | Next step |
| --- | --- | --- |
| No new tasks | Registration, stake, reserved case collateral, and new requests on the network | No assignments alone do not prove downtime; verify candidate eligibility |
| RPC timeouts or frozen external height | Network, source height, connectivity, and rate limits | Repair or switch to a verified source; do not guess votes |
| Failed vote | Final receipt, current round, deadline, balance, and nonce | Check inclusion before retrying a still-valid, unexecuted task |
| Exit stake cannot be claimed | Lock period and all old case duties | Finish existing duties and claim when eligible |` },
    ],
  },
  {
    id: 'node-mining-overview',
    title: 'Node Mining Overview',
    summary: 'Node miners run full nodes, validate transactions, vote in consensus, and produce blocks when eligible, helping maintain network finality through correct and timely participation.',
    params: [
      { name: 'Core duty', value: 'Stay synchronized, validate blocks, participate in consensus, and produce blocks when eligible' },
      { name: 'Minimum stake', value: 'Currently 32 FB by default' },
      { name: 'Addressing', value: 'DEX accounts use fbc1; advertise_addr must be reachable by peers' },
      { name: 'Main risk', value: 'Downtime reduces earnings; equivocation or key compromise can cause removal or slashing' },
    ],
    sections: [
      { heading: 'Participation requirements', bullets: ['Use the Recommended Hardware and Capacity baseline: SSD storage is mandatory, with at least 1.5 TB allocated for node data.', 'The node hot address owns and locks miner stake. Choose a long-lived fixed reward address; both rewards and remaining exit principal go there.', 'Use the correct node keys, chain ID, environment, and advertised address.', 'Finish synchronization and validate node health before joining mining.'] },
      { heading: 'Not the same as a witness', body: 'Node mining maintains DEX consensus and block production; witness mining verifies external deposits. A node can register separately as a witness, but the duties and risks remain distinct.' },
    ],
  },
  {
    id: 'node-mining-hardware',
    title: 'Recommended Hardware and Capacity',
    summary: 'For one DEX miner node, use a dedicated Linux host with 10 physical cores / 20 threads and 64 GB RAM. SSD storage is mandatory with at least 1.5 TB allocated for node data; prefer NVMe. Public bandwidth must be at least 100 Mbps.',
    params: [
      { name: 'Recommended CPU', value: '10 physical cores / 20 threads; Intel Xeon Gold 5115 @ 2.40 GHz or equivalent or better performance' },
      { name: 'Recommended memory', value: '64 GB RAM' },
      { name: 'Required storage', value: 'SSD with node data capacity ≥ 1.5 TB; prefer NVMe and do not use HDD for node data' },
      { name: 'Recommended OS', value: 'Ubuntu 24.04 LTS, x86_64' },
      { name: 'Networking', value: 'Public bandwidth ≥ 100 Mbps (about 12.5 MB/s), stable public endpoint; example port 6000/TCP and 6000/UDP, subject to network configuration' },
    ],
    sections: [
      { heading: 'What this baseline covers', body: 'This is a recommended deployment baseline for one DEX node, not a measured minimum or a promise of TPS, sync time, or returns. Consider CPU model, physical cores, and memory together; shared vCPU counts do not guarantee equivalent sustained performance. Allocate additional resources for Explorer indexing, load generators, or external-chain nodes.' },
      { heading: 'Capacity and headroom', bullets: [
        '1.5 TB means allocated node data capacity, not 1.5 TB permanently free. Account separately for the OS and other services.',
        'Databases, checkpoints, query history, temporary sync files, and logs all require space. Initial database size is not a capacity forecast.',
        'Keep at least 20% free and alert before reaching that point as an operational recommendation. Use observed daily growth and remaining days to plan expansion.',
        'Verify the SSD mount and filesystem. Docker named volumes follow the Docker data directory; adding an SSD does not move an existing volume.',
        'Self-hosted Bitcoin, EVM, or TRON nodes require separate SSD, memory, and sync headroom. The 1.5 TB baseline does not include their databases.',
      ] },
      { heading: 'Network and isolation', bullets: [
        'The registered public host:port must be reachable by other nodes over the configured TCP and UDP ports. Do not advertise localhost, private addresses, or endpoints without working inbound routing.',
        'Public bandwidth must be at least 100 Mbps (about 12.5 MB/s). Check the provider’s public allowance, shared limits, and traffic caps, maintain stable bidirectional connectivity, and monitor throughput, packet loss, and peer latency. A negotiated NIC rate such as 10 GbE does not establish guaranteed public bandwidth.',
        'Restrict management access to operator sources; do not expose SSH, secret services, or debug endpoints without access controls.',
        'Run one identity per process. For FROST, run at most one participant on a host and distribute participants across independent control domains.',
      ] },
      { heading: 'Read-only host checks', body: 'ROTA=0 reports non-rotating media. For cloud disks, also verify the provider’s SSD type and sustained I/O specifications. Use df to inspect the actual data filesystem and mount.', codeLabel: 'Bash', code: `lscpu
free -h
lsblk -d -o NAME,MODEL,SIZE,ROTA
df -hT
sudo docker info --format '{{.DockerRootDir}}'` },
    ],
  },
  {
    id: 'node-mining-deploy-join',
    title: 'Quick Node Deployment',
    summary: 'Use the network-published digest-pinned Docker image with no source checkout, Git, or local build. After preparing network configuration and key files, two commands start synchronization and complete initial registration. These examples target Linux / Ubuntu.',
    params: [
      { name: 'Prepare', value: 'Ubuntu host, frostbit-node from the release, the published image digest and five configuration files, and separately stored key files' },
      { name: 'Open ports', value: '6000/TCP and 6000/UDP' },
      { name: 'Minimum total stake', value: 'Currently 32 FB by default' },
      { name: 'Automated', value: 'Pinned-image pull, strict configuration checks, address derivation, unique public-IP selection, secret installation, node startup, and duplicate-safe registration' },
    ],
    sections: [
      {
        heading: 'Prepare once',
        body: 'From the target network release, obtain frostbit-node, the image reference including its sha256 digest, and the matching config.json, genesis.json, frost_default.json, witness_default.json, and peers.json. Put the five public files in one directory and keep the node secp256k1 private key outside the release directory. When FROST is enabled, retain the original independent 32-byte wrap key: the tool never derives, generates, or overwrites it. Use an independent long-lived fbc1 reward address.',
        codeLabel: 'Bash',
        code: `sudo apt-get update
sudo apt-get install -y docker.io
sudo systemctl enable --now docker
cd /path/to/extracted/frostbit-release`,
        bullets: [
          'The private-key file must contain exactly one 64-character hexadecimal token, must not be a symlink, and must not be group/world-writable. Never put the key in argv, environment variables, the repository, or an image.',
          'Fund the derived hot address with at least 32 FB plus fees. 3200000000 raw units equals 32 FB.',
          'Use the versioned image and SHA-256 digest from the release page. If the target network has yet to provide the matching artifacts, wait for its release before running the commands below.',
          'A template or configuration from another network cannot replace the published trust root.',
        ],
      },
      {
        heading: '1. Pull the image and start synchronization with one command',
        body: 'frostbit-node pulls the release image by digest, validates all five public files, chain ID, and trust manifest inside that image, and passes the private key through a read-only file descriptor to derive the miner address. A single usable public IPv4 is selected automatically; NAT, multiple public addresses, or ambiguous detection require an explicit inbound-mapped endpoint. No source checkout, Git installation, or Go toolchain is required.',
        codeLabel: 'Bash',
        code: `sudo ./frostbit-node up \\
  --image REGISTRY.example/frostbit-node:vX.Y.Z@sha256:REPLACE_WITH_RELEASE_DIGEST \\
  --network-dir /secure-import/network-release/config/runtime \\
  --private-key-file /secure-import/miner/private_key \\
  --frost-wrap-key-file /secure-import/miner/frost_wrap_key \\
  --reward-address REPLACE_WITH_FIXED_FBC_REWARD_ADDRESS \\
  --explorer-url https://REPLACE_WITH_NETWORK_EXPLORER`,
        bullets: [
          'Omit --frost-wrap-key-file when FROST is disabled. Existing FROST data must keep its original wrap key; a database backup cannot decrypt signing shares without it.',
          'For NAT, multiple public IPs, or failed automatic selection, append --advertise-endpoint YOUR_PUBLIC_IPV4:6000. The script never calls a third-party “what is my IP” service.',
          'Image ID, configuration digest, identity, and endpoint are bound to the container and data volume. A mismatched rerun fails instead of silently replacing them or creating another identity.',
        ],
      },
      {
        heading: '2. Register after synchronization',
        body: 'Inspect the logs first. After synchronization, same-height block-ID agreement, and public TCP/UDP reachability are verified, run registration. --confirm-ready acknowledges those external checks; registration is the only phase that signs and submits the 32 FB stake transaction.',
        codeLabel: 'Bash',
        code: `sudo docker logs -f --tail 100 frostbit-node

# After acceptance, run this in another terminal:
sudo ./frostbit-node register --confirm-ready`,
        bullets: [
          'The container is running and the node is not syncing; finalized height advances and its block ID matches several trusted nodes at the same height.',
          'From another host, verify inbound TCP and UDP reachability for the advertised endpoint and check that peer, signature, state-root, and storage errors are not persistent.',
          'The hot address can fund 32 FB plus fees, and you verified the reward address, Explorer network, and chain ID.',
          'A dynamic one- or two-block lag is display state, not a permanent registration lock. What matters is that synchronization is inactive and the common-height block ID matches.',
          'Registration first queries current miner state. An exact identity, reward, and endpoint match is a no-op; any mismatch stops before MinerTx ADD can become an unintended top-up.',
          'Submitted only means submitted. Save and query tx_id, then verify its final receipt and MinerLockedBalance in Explorer.',
          'After a timeout or disconnect, normal register refuses to create a new nonce. Only for an unresolved result, append --resubmit-unresolved; it sends the identical raw transaction from the state file.',
          'Successful first registration starts in Probation. Stay synchronized and publicly reachable until protocol conditions promote it to Active; immediate block production is not guaranteed.',
          'An already registered node does not register again on restart. Repeating the same command is a no-op only when the on-chain identity matches exactly.',
        ],
      },
      {
        heading: 'Operations and security boundary',
        body: 'The tool installs configuration and keys at fixed read-only mount locations and uses a volume bound to the miner address, chain ID, image, and configuration digest. Use a trusted secret-fetch workflow when keys must not persist on the VPS. Never delete the state directory to bypass unresolved-transaction protection. For the native Linux binary, see [Miner Key Encryption and Manual Unlock](#docs_center/node-mining-keystore).',
        codeLabel: 'Bash',
        code: `sudo docker stop frostbit-node
sudo docker start frostbit-node
sudo docker restart frostbit-node`,
      },
    ],
  },
  {
    id: 'node-mining-keystore',
    title: 'Miner Key Encryption and Manual Unlock',
    summary: 'Migrate a registered miner on Linux to an encrypted keystore with the native dex binary. Enter the passphrase at startup to unlock the same miner identity.',
    params: [
      { name: 'Startup', value: 'Linux dex release binary with keystore flags and an interactive terminal' },
      { name: 'Source file', value: 'Private regular file with one 64-character hexadecimal key' },
      { name: 'Plaintext cleanup', value: 'The node keeps the source by default. On Linux, opt in to cleanup after startup.' },
    ],
    sections: [
      {
        heading: 'Switch the key source and start the node',
        body: 'Stop the old instance and confirm it has exited. In the native dex binary, use `./dex --help` to check for `--keystore`, `--keystore-import-from`, and `--delete-keystore-import-source`. Replace the miner key FD argument with the keystore arguments. Keep the existing network configuration, peers, bootstrap mode, listen and advertised addresses, miner address, data directory, and `--resume=true`. Continue supplying the original FROST wrap key through its own FD when FROST is enabled.',
        codeLabel: 'Bash · Registered latejoin miner with FROST disabled',
        code: `MINER_ADDRESS='REPLACE_WITH_REGISTERED_FBC_ADDRESS'
MINER_ENDPOINT='YOUR_PUBLIC_IPV4:6000'
./dex \\
  --config-dir /etc/dex/miner \\
  --peers /etc/dex/miner/peers.json \\
  --bootstrap-mode latejoin \\
  --advertise-addr "$MINER_ENDPOINT" \\
  --expected-address "$MINER_ADDRESS" \\
  --data-dir "/var/lib/dex/$MINER_ADDRESS" \\
  --resume=true \\
  --keystore /etc/dex/miner.v3.json \\
  --keystore-import-from /secure-import/miner.key \\
  --no-simulator`,
        bullets: [
          'Use private paths and create the keystore parent directory before startup. Set the source file to mode 0600. Use the actual configuration, address, endpoint, and data directory for the miner.',
          'On first import, enter the same passphrase twice in the terminal. The passphrase must be at least 12 bytes. The node writes the keystore with private permissions, decrypts it to verify the key, then starts. On later starts, enter the passphrase once.',
          'Manual unlock requires an interactive terminal. A regular systemctl start does not send the prompt to the terminal of the caller. When using manual unlock, arrange an operator for each startup and disable unattended restarts with Restart=no.',
          'Check synchronization, same-height block IDs, and public TCP and UDP reachability as described in [Quick Node Deployment](#docs_center/node-mining-deploy-join). Startup readiness and mining eligibility are separate checks.',
        ],
      },
      {
        heading: 'Optional cleanup after startup',
        body: 'After confirming that a separate offline backup can recover the miner key, add `--delete-keystore-import-source` after `--no-simulator` in the same Linux command. Keep both `--keystore` and `--keystore-import-from`. The node verifies that the keystore unlocks to the source key, waits until its listeners are ready, and syncs the keystore and its directory before deleting the specified plaintext file.',
        bullets: [
          'Keep the source and keystore paths under the control of root or the node account. Cleanup requires a private regular source file with a single hard link. If startup fails or the files fail verification, the source remains available.',
          'Fix the failure and retry the same command. An existing keystore unlocks before the node checks the source for cleanup. With the source already removed, the same command unlocks and starts normally.',
          'Cleanup covers the specified source path. Manage Docker-installed plaintext copies, backups, and snapshots separately. Keep a controlled recovery copy for registration or endpoint tools that take a private-key FD.',
        ],
      },
    ],
  },
  {
    id: 'node-mining-consensus-rewards',
    title: 'Consensus, Blocks, and Rewards',
    summary: 'An active miner participates in consensus and proposes a block when eligible. Not producing a recent block is not itself a failure; continuous correct participation is what matters.',
    params: [
      { name: 'Producer share', value: 'Currently 50% of the block reward goes to that block producer' },
      { name: 'Active-miner share', value: 'The remaining 50% is shared equally by other active miners' },
      { name: 'Source of truth', value: 'Use on-chain block reward records and account activity' },
    ],
    sections: [
      { heading: 'Normal operation', bullets: ['The node continuously receives and validates transactions, blocks, and consensus messages.', 'It samples and votes each round, and builds, signs, and broadcasts a candidate when proposer-eligible.', 'Finalization commits state and distributes rewards, visible in explorer reward records.', 'An active miner that did not produce the block may still receive the active-miner share.'] },
      { heading: 'Evaluate returns', body: 'Returns depend on active miner count, uptime, proposer eligibility, and network reward parameters. Measure producer rewards, active shares, operating costs, and slashing over a meaningful period.' },
    ],
  },
  {
    id: 'node-mining-operations',
    title: 'Daily Operations and Security',
    summary: 'Node miners continuously monitor synchronization, consensus, resources, and key safety to identify degrading trends before block production stops.',
    params: [
      { name: 'Healthy', value: 'Finalized height advances, peers are stable, latency matches the network, and validation errors do not persist' },
      { name: 'Resources', value: 'CPU, memory, free disk, disk latency, network, and log growth' },
      { name: 'Record', value: 'Height, state, resources, peer count, recent changes, incident start, and recovery time' },
    ],
    sections: [
      { heading: 'Checklist', bullets: ['Local finalized height and state agree with several trusted peers.', 'Peer count, consensus traffic, recent blocks, and transaction pool are healthy.', 'CPU, memory, disk, network, and file logs are not persistently saturated or growing abnormally.', 'Synchronization, storage flushes, conservation audit, and protocol workers have no persistent errors.', 'Stake, node hot address, and reward recipient remain correct.'] },
      { heading: 'Offline grace period for active miners', bullets: [
        'The chain records the first height H with sufficient offline evidence. The grace period is 17,280 blocks, about 24 hours at five seconds per block. From H + 17,280 onward, removal from the active set still requires sufficient offline evidence at that time. The block count stops advancing when the chain stops producing blocks.',
        'A verified signed response or a finalized block produced by the miner clears the offline starting height. An unknown status or too few samples can neither clear it nor trigger removal on its own. Check synchronization, signed responses, and on-chain miner status together.',
        'Offline removal does not slash the stake. See [Mining Exit and Incident Response](#docs_center/node-mining-exit-incident) for release conditions and remaining vault duties.',
      ] },
      { heading: 'Security baseline', body: 'Restrict service exposure, keep offline backups of configuration and key material, run with least privilege, and record changes. Never bypass signature, state-root, VRF, frontier, or block validation.' },
      { heading: 'Restart a node that uses a keystore', body: 'Plan for an operator to enter the passphrase at each startup and launch the process with an interactive terminal. See [Miner Key Encryption and Manual Unlock](#docs_center/node-mining-keystore) for the native startup command.' },
    ],
  },
  {
    id: 'node-mining-maintenance',
    title: 'Upgrades, Backups, and Host Migration',
    summary: 'Preserve identity and data continuity during upgrades, and stop the old instance before migration. Configuration, keys, database recovery, and FROST signing state have distinct requirements; do not resume signing from an old snapshot.',
    params: [
      { name: 'Preserve', value: 'Version, public configuration, miner and reward addresses, key material, data volume, and startup arguments' },
      { name: 'For FROST', value: 'Keep the original wrap key, encrypted shares, and latest nonce state together' },
      { name: 'Runtime rule', value: 'Only one instance may sign for an identity; keep --resume=true on restart' },
    ],
    sections: [
      { heading: 'Backup inventory', body: `| Material | Purpose and storage |
| --- | --- |
| Miner address, fixed reward address, version, and image ID | Check recovered identity and registration; record public metadata separately |
| config/runtime and startup arguments | Restore the same chain ID, genesis trust set, endpoint, and directories; record configuration version |
| Node private key and FROST wrap key | Store offline or in an independent trusted key service with restricted access; never upload in logs or support tickets |
| Encrypted miner keystore and passphrase | Back up the keystore offline and store the passphrase separately. Verify that the backup unlocks and derives the same miner address. |
| Complete persistent data volume | Take a consistent copy after writes stop, including local encrypted FROST shares and nonce state |
| Logs and maintenance records | Establish incident timing; remove sensitive data before sharing |` },
      { heading: 'Built-in updates for native Linux nodes', bullets: [
        'Run ./dex --help to check for --auto-update. On supported builds, checks run in the background every ten minutes by default. A candidate must come from an official immutable GitHub Release and match the chain, platform, protocol, and database requirements. Verification requires a GitHub CLI with gh release verify-asset. The current version stays running if no compatible release can be verified.',
        'A node exits on its own for an update only when it verifies that its systemd service restarts after exit code 75 and the node does not use --keystore. A manually started node or a node using a keystore continues running until a clean shutdown. The update takes effect on the next startup, when manual unlock still requires the operator to enter the passphrase in a terminal.',
        'The new version is confirmed only after listener, chain progress, and persisted state checks pass. If the candidate fails, the next launch through the original entry point returns to the previously confirmed version. --auto-update=false disables new checks while keeping the confirmed cached version available. Follow the network announcement for coordinated protocol upgrades.',
      ] },
      { heading: 'Planned upgrade for digest-pinned Docker nodes', bullets: [
        'Read the target network upgrade instructions and check chain parameters, database compatibility, and migration requirements. Record the current image ID, configuration version, finalized height, and matching block ID.',
        'Pull the new digest-pinned image from the official release and review its upgrade notes while the current node keeps running. Do not replace the published digest with a locally built candidate tag.',
        'Stop this node and confirm the process exits before saving a consistent data copy and configuration. Do not stop all miners together.',
        'After the old container is stopped and backed up, `sudo docker rm frostbit-node` may release its name, but never add -v. frostbit-node up intentionally rejects an image different from the original deployment and is not an upgrade command. Follow the target release migration procedure, preserving the identity, secrets, named volume, and --resume=true; never delete intent state to bypass the guard.',
        'Verify logs, endpoint reachability, synchronized height, same-height block IDs, miner status, and FROST workers before upgrading another node.',
        'After a failed upgrade, stop the new instance and preserve errors. Revert the executable only if it supports the current data format; do not overwrite data with the pre-upgrade database.',
      ], codeLabel: 'Bash · Read-only records before stopping', code: `sudo docker inspect --format '{{.Image}}' frostbit-node
sudo docker inspect --format '{{json .Mounts}}' frostbit-node
sudo docker logs --tail 100 frostbit-node` },
      { heading: 'Host migration and recovery', bullets: [
        'Check the new host against SSD ≥ 1.5 TB and the other requirements, then prepare the same version, configuration, and key access method.',
        'Stop the old instance and disable its startup path to prevent automatic restart. Copy the latest complete, consistent volume from that stopped instance before starting the replacement.',
        'The private key must derive the same address used by --expected-address, the data directory, and registration. Do not randomly regenerate an existing FROST wrap key.',
        'Restore the miner keystore and unlock it with the same passphrase. For FROST, use the original wrap key and a consistent data backup containing the latest shares and nonce state. See [Miner Key Encryption and Manual Unlock](#docs_center/node-mining-keystore).',
        'Keep the registered public endpoint reachable where possible. If it changes, verify the current chain-supported update process first; changing advertise-addr alone does not change registration.',
        'Repeat the synchronization acceptance checks. Do not register or stake again, and never restart the old host as a second signer for the same identity.',
      ] },
      { heading: 'Never roll back FROST nonces', body: 'Nonce usage records are local signing safety state. Resuming from an older disk or cloud snapshot can reuse a nonce. Even a verified chain checkpoint cannot replace missing local shares or nonce records. If you cannot prove the copy is the latest consistent state after signing stopped, keep signing disabled and follow the DKG / key-transition maintenance process. Repeatedly restoring old snapshots is not a recovery method. Neither keys alone nor a database alone constitutes a complete FROST backup.' },
      { heading: 'Low disk space', body: 'Identify the data mount and database versus log usage, then expand the SSD or use supported history-retention settings. Do not delete StateDB, FROST data, nonce records, or live WAL / SST files; do not run Docker volume prune against node volumes. After expansion, check filesystem capacity, writes, and advancing finalized height.' },
    ],
  },
  {
    id: 'node-mining-sync-slow',
    title: 'Lagging Sync and Slow Blocks',
    summary: 'A small lag usually catches up automatically. Deep lag may use checkpoint-first state synchronization before batch catch-up. Slow blocks require separating network-wide delay from one node’s resources.',
    params: [
      { name: 'Small lag', value: 'Watch the gap shrink; avoid repeated restarts' },
      { name: 'Deep lag', value: 'Inspect sync mode, anchor, target, frontier, and checkpoint progress' },
      { name: 'Danger', value: 'Frozen height, root mismatch, repeated validation failure, or disagreement among trusted peers' },
    ],
    sections: [
      { heading: 'Investigation order', bullets: ['Compare several trusted nodes to distinguish local lag from network-wide slow production.', 'Inspect peer connectivity, sync stage, target height, and whether the gap is shrinking.', 'Check CPU, memory, disk latency, free space, network, and recent configuration changes.', 'For periodic slow blocks, inspect funding, liquidation, rent, audit, and flush timing.', 'Preserve recent-block, BlockTrace, and resource data before rate limiting, repair, or restart.'] },
      { heading: 'Recovery rule', body: 'Synchronization must use quorum-backed frontier data and full block validation. Never force state from an arbitrary peer, delete the database, lower consensus thresholds, or force a reorganization for superficial progress.' },
    ],
  },
  {
    id: 'node-mining-exit-incident',
    title: 'Exit Mining and Incident Response',
    summary: 'Redeem from the miner’s hot wallet to end mining on-chain. The network returns any remaining principal automatically after the freeze and any applicable vault handover. Check outstanding duties before shutting down.',
    params: [
      { name: 'Entry point', value: 'Trade → Account → Staking (Mining) → Redeem' },
      { name: 'Freeze', value: '100 blocks from the exit transaction’s execution height. A pending vault handover can delay release.' },
      { name: 'Return destination', value: 'The fixed payout address bound to the miner. The network returns any remaining principal automatically.' },
      { name: 'Priority', value: 'Key and asset safety > network finality > availability > display consistency' },
      { name: 'Evidence', value: 'Configuration, logs, database state, heights, block IDs, resource metrics, and action history' },
    ],
    sections: [
      {
        heading: '1. Redeem from the miner account',
        bullets: [
          'Connect the miner’s hot wallet on the correct network. In **Trade → Account → Staking (Mining)**, check the locked FB balance and the fixed payout address. Keep enough available FB to pay the transaction fee.',
          'Select **Redeem** and confirm the wallet signature. This requests exit for the whole mining stake. Save the TxID and check for finalized success in transaction details.',
          'Record the displayed exit height `H` and check that the account has entered the exit state. Use the receipt to distinguish a completed exit request from a pending or failed one.',
        ],
      },
      {
        heading: '2. Verify the automatic return',
        bullets: [
          'The earliest release is at finalized height `H + 100`. Pending vault handover can keep the principal locked beyond that height. Follow finalized height and the locked balance as the network progresses.',
          'The network returns the actual remaining principal to the fixed payout address. An older account without a separate payout address receives it at its hot address. Check the recipient’s available FB in address details, or connect that wallet and look for **Stake Unlocked** in **My Messages**. Confirm the mining lock at the hot address has cleared. Check block rewards separately at the payout address.',
          'Offline removal starts automatic unbonding. Check the exit height, locked balance, and any recorded slashing before submitting a new transaction. Slashing can reduce the amount ultimately returned.',
        ],
      },
      {
        heading: '3. Finish any other duties before shutdown',
        bullets: [
          'A miner that only participates in consensus can stop its node after the exit request finalizes successfully and its remaining signing duties are complete. The network processes the later release even while that node is offline.',
          'If the miner serves a FROST vault, open **Protocol → Vault DKG** and review the active epoch, old and new committees, and handover status. The latest DKG’s new committee list covers that round. Keep the signing service, original shares, and latest nonce state available until your current and transitional duties end. Check these duties even if mining stake has already been released.',
          'If the account also serves as a witness, complete [the witness exit and claim process](#docs_center/witness-mining-join-exit) separately.',
        ],
      },
      {
        heading: 'When redeem or release is blocked',
        bullets: [
          'An active DKG session or pending vault handover can block Redeem. Check the receipt and the relevant vault’s handover progress before retrying.',
          'If Redeem is disabled or the transaction failed, check the hot address, network, locked stake, available FB, and the original TxID. During a confirmed exit, monitor the existing lock and payout instead of redeeming again.',
        ],
      },
      { heading: 'Incident response', body: 'If a node hot key may be compromised, isolate the instance and stop further signing. Preserve state and compare with several trusted peers. Prefer a verified checkpoint or documented repair path; do not delete data, force a reorg, or overwrite a live database with an old backup.' },
    ],
  },
  {
    id: 'safety-tips',
    title: 'Safety Tips',
    summary: 'Protect signing authority, verify every address and network, and use conservative operational procedures.',
    params: [
      { name: 'Users', value: 'Protect wallet keys and verify every transaction, chain, asset, address, and amount' },
      { name: 'Miners', value: 'Separate hot and cold keys, restrict services, monitor continuously, and preserve evidence' },
      { name: 'Shared control', value: 'Use explicit roles, independent verification, approval records, and recovery procedures' },
    ],
    sections: [
      { heading: 'User safety', bullets: ['Verify chain, asset, address, amount, fee, and order side before signing.', 'Never share seed phrases or private keys.', 'Use transaction and request IDs to track results instead of repeatedly submitting.'] },
      { heading: 'Operator safety', bullets: ['Back up configuration and key material securely.', 'Restrict service exposure and monitor disk pressure.', 'Do not bypass signature, root, VRF, frontier, or template validation.'] },
    ],
  },
  {
    id: 'faq',
    title: 'FAQ and Troubleshooting',
    summary: 'Locate the original operation and stage before retrying. Refreshing a read, resubmitting proof, and making another payment are different actions.',
    params: [
      { name: 'First step', value: 'Save DEX transaction ID, request ID, and external hash' },
      { name: 'Unknown result', value: 'Inspect the original record before creating another request' },
      { name: 'DEX fees', value: 'Runtime fees use available FB, not an external-wallet balance' },
    ],
    sections: [
      {
        heading: 'Wallets, networks, and fees',
        body: '| Symptom | Check | Next action | Retry condition |\n| --- | --- | --- | --- |\n| Wallet absent / no account | Extension enabled, unlocked, and allowed on this site | Connect the correct wallet and verify its address | Reconnect after handling any existing signing popup |\n| Wrong network / recipient | Wallet network, selected chain, full recipient | Switch before signing; inspect the original transaction if sent | Switching networks does not undo a payment |\n| Token decimals unavailable or changed | Contract, selected external wallet, and network | Identify again and review raw units; do not guess decimals | After a successful read if nothing was submitted |\n| Balance exists but fees are insufficient | Available FB, frozen funds, external currency / resources | Fund the fee balance required for this operation | After confirming failure or no submission |\n| Rejected prompt / network timeout | Wallet activity and DEX or external transaction ID | Determine whether it was actually sent | Only after no submission or confirmed failure |',
      },
      {
        heading: 'Deposits, withdrawals, and refunds',
        body: '| Symptom | Check | Next action | Retry condition |\n| --- | --- | --- | --- |\n| External payment succeeded, no DEX credit | BTC txid/vout or deposit event, then DEX request | Inspect witnessing, deadline, and disputes | Reuse original proof only when failure is confirmed and the page allows it; do not pay again |\n| No automatic BTC request | Watch registration, expiry, and mining time | Report txid/vout manually if watching was unavailable; retain evidence for watch errors | Do not resubmit if a request already exists |\n| Approval succeeded, no deposit event | Whether the transaction was only token approval | Continue the original contract deposit flow | First confirm no deposit transaction was already sent |\n| Withdrawal remains SIGNED | Original step two, Vault state, or BTC txid | Claim with an account-chain wallet or check BTC broadcast | Continue the existing withdrawal; do not request another debit |\n| External claim failed or unchecked | Original hash, Vault execution, and signing epoch | Fix fees or network; restore unavailable RPC reads | Retry the same claim only if unexecuted and the signature remains valid |\n| Cancellation failed | Whether Claim / SIGNING already started | Continue signing and claiming if Claim won the race | Only a queued, unclaimed request can still cancel; clicking is not a refund guarantee |',
      },
      {
        heading: 'Balances, rent, and transaction results',
        body: '| Symptom | Check | Next action | Retry condition |\n| --- | --- | --- | --- |\n| Available balance below total | Frozen orders, margin, stake, and activity | Reconcile fills and remaining quantity; manage the relevant order or position | Wait for final release before spending the balance |\n| Rent warning / cancelled orders | Rent status, recycle height, and required funding in Account | Add the required FB before cleanup starts. If cleanup is in progress, stop funding the address and check its activity history | Confirm rent status and spendable FB first |\n| Failed DEX transaction / nonce error | Receipt, active wallet account, and earlier pending transactions | Correct funds, fees, or parameters. For a transfer nonce conflict, check the earlier transaction before refreshing the nonce | Check the original receipt and any runtime fee before retrying |\n| Position remains after Close | Close transaction, fills, and remaining position | Distinguish pending, partial execution, and failure | Inspect the previous request before acting on the remainder |',
      },
      {
        heading: 'If further investigation is needed',
        body: 'Provide network, account address, DEX transaction ID, request ID, external hash, asset, amount, current state, and full error. Screenshots can help explain the error, but exclude private keys, seed phrases, passwords, and wallet backups.',
      },
    ],
  },
  {
    id: 'glossary',
    title: 'Glossary',
    summary: 'Definitions of the terms used throughout the explorer and documentation.',
    params: [
      { name: 'Finalized block', value: 'A block accepted as the authoritative chain state' },
      { name: 'Order book', value: 'The currently executable set of buy and sell orders' },
      { name: 'Slippage', value: 'The difference between expected and actual execution price' },
      { name: 'Funding', value: 'Periodic value transfer between perpetual longs and shorts' },
      { name: 'Liquidation', value: 'Protocol-driven position closure when risk is too high' },
      { name: 'Vault', value: 'A distributed custody and signing unit for cross-chain assets' },
    ],
    sections: [
      { heading: 'Additional terms', bullets: ['Nonce: per-account sequence number used to prevent duplicate execution.', 'Witness: participant that observes and verifies cross-chain events.', 'Checkpoint: certified state snapshot used as a deep-sync trust anchor.', 'State root: commitment to the authoritative execution state.'] },
    ],
  },
]

export const englishTreeTitles: Record<string, string> = {
  start: 'Quick Start',
  'docs-home-node': 'Documentation Overview',
  'first-use-node': 'First Use: Complete Your First Operation',
  'parameter-cheatsheet-node': 'Parameter Cheat Sheet',
  'roles-entry-node': 'Roles and Entry Points',
  'core-concepts-node': 'Core Concepts',
  account: 'Accounts and Assets',
  'wallet-extension-node': 'Install and Use the Wallet Extension',
  'wallet-connect-node': 'Connect a Wallet',
  'asset-address-node': 'Assets and Addresses',
  'account-rent-node': 'Account Rent',
  'activation-fee-node': 'Account Activation Fee',
  trading: 'Trading',
  spot: 'Spot',
  'spot-trading-node': 'Spot Trading',
  futures: 'Perpetual Futures',
  'futures-trading-node': 'Perpetual Futures',
  'funding-rate-node': 'Funding Rate',
  'liquidation-risk-node': 'Liquidation Risk',
  'fees-rebates-node': 'Fees and Rebates',
  'cross-chain': 'Cross-chain Deposits and Withdrawals',
  'deposit-flow-node': 'Deposit Flow',
  'withdraw-flow-node': 'Withdrawal Flow',
  'vault-signing-node': 'Vaults and Signing',
  'cross-chain-status-node': 'Track Cross-chain Progress',
  'cross-chain-challenge-node': 'Deposit Challenges and Arbitration',
  'explorer-use': 'Using the Explorer',
  'explorer-backend-node': 'Install and Run the Explorer Backend',
  'search-records-node': 'Search Transactions, Blocks, and Accounts',
  'node-health-node': 'Node Health',
  'activity-history-node': 'Activity and History',
  miners: 'Miners',
  'witness-mining': 'Witness Mining',
  'witness-mining-overview-node': 'Role and Requirements',
  'witness-mining-join-exit-node': 'Join, Stake, and Exit',
  'witness-mining-workflow-node': 'Witness Tasks and Voting',
  'witness-mining-rewards-risk-node': 'Rewards, Challenges, and Slashing',
  'witness-mining-operations-node': 'Operations and Troubleshooting',
  'node-mining': 'Node Mining',
  'node-mining-overview-node': 'Role and Requirements',
  'node-mining-hardware-node': 'Hardware and Capacity Planning',
  'node-mining-deploy-join-node': 'Quick Node Deployment',
  'node-mining-keystore-node': 'Miner Key Encryption and Manual Unlock',
  'node-mining-consensus-rewards-node': 'Consensus, Blocks, and Rewards',
  'node-mining-operations-node': 'Operations and Security',
  'node-mining-maintenance-node': 'Upgrades, Backups, and Migration',
  'node-mining-sync-slow-node': 'Lagging Sync and Slow Blocks',
  'node-mining-exit-incident-node': 'Exit and Incident Response',
  'risk-faq': 'Risk and FAQ',
  'safety-tips-node': 'Safety Tips',
  'faq-node': 'Frequently Asked Questions',
  'glossary-node': 'Glossary',
}
