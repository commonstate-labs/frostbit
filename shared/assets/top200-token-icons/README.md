# Top-200 token icons (2026-09-24 snapshot)

`../../top200TokenIcons.snapshot.json` records the UTC capture time, SHA-256 of the CoinGecko market-ranking response, all 200 ranked coin IDs, accepted chain/contract identities, exact Trust Wallet asset records and logo URLs, and excluded candidates. The generated `../../top200TokenIcons.mjs` is the frontend catalog; run `explorer/scripts/snapshot-top200-token-icons.mjs` only when deliberately refreshing this reviewed snapshot.

- Ranking candidate source: [CoinGecko coins/markets API](https://docs.coingecko.com/reference/coins-markets), ordered by USD market cap.
- Contract and image cross-check: [Trust Wallet assets](https://github.com/trustwallet/assets). A candidate is included only if its exact chain and contract has an asset record with the same symbol and a valid local PNG. The repo's MIT license is copied as `TRUSTWALLET-LICENSE`; project logos can also be trademarks of their respective owners.
- Additional UNI Base and BNB deployments: [Uniswap's contract address list](https://developers.uniswap.org/docs/ecosystem/governance/uni).

The catalog covers 138 supported-chain addresses belonging to 81 of the ranked projects. Many top-200 projects have no address on ETH, BNB, Base, Arbitrum, or TRON; other candidates lacked the required second source or image. The list is for display only. It never authorizes a deposit, determines decimals, or changes the chain's AssetID.
