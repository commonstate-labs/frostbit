# FrostBit Wallet — Chrome 插件

## 目录结构

```
wallet/
├── manifest.json          # Chrome Extension Manifest V3
├── package.json           # 依赖（protobufjs、bip39、bip32、esbuild）
├── build.js               # 构建脚本
├── background/
│   └── service_worker.js  # 密钥管理、签名、RPC 中枢
├── content/
│   └── content_script.js  # 注入 window.frostbit Provider
├── popup/
│   ├── index.html
│   ├── popup.js
│   └── popup.css
├── lib/
│   ├── crypto.js          # secp256k1 签名工具
│   ├── keystore.js        # BIP-39/32 推导 + AES-GCM 加密存储
│   ├── proto.js           # protobufjs AnyTx 序列化 + 签名组装
│   ├── rpc.js             # 节点 HTTP API 封装
│   └── data_proto.json    # proto 编译产物（见下方步骤生成）
├── pb/
│   └── data.proto         # 从 ../pb/data.proto 复制
└── icons/
    ├── icon16.png
    ├── icon48.png
    └── icon128.png
```

## 快速开始

### 1. 安装依赖

```bash
cd wallet
npm install
```

### 2. 编译 proto 描述符

```bash
# 安装 pbjs 工具（已随 protobufjs 安装）
npx pbjs -t json pb/data.proto -o lib/data_proto.json
```

### 3. 构建插件

```bash
node build.js
# 产物在 dist/ 目录
```

### 4. 加载到 Chrome

1. 打开 `chrome://extensions/`
2. 开启右上角「开发者模式」
3. 点击「加载已解压的扩展程序」
4. 选择 `wallet/dist/` 目录（或直接选 `wallet/` 目录，若直接使用源文件）

## 注意事项

### 账户管理与私钥导入

钱包解锁后可从首页的「账户」入口管理地址：

- 助记词钱包可以按 `m/44'/9999'/0'/0/<index>` 的下一个可用路径创建新地址。
- 所有钱包都可以继续导入 32 字节 secp256k1 私钥；支持 64 位 hex、`0x` 前缀、每行一个私钥和 `#` 注释。
- 新增私钥使用当前钱包密码经 PBKDF2 + AES-GCM 加密保存，解锁后的导入界面不会再次收集或传递密码。
- 私钥输入中的无效行会明确报错，不会被静默忽略；重复私钥和重复地址不会重复保存。

### secp256k1 私钥推导

`lib/keystore.js` 通过 `@scure/bip32` 推导 BIP-32 子节点的 32 字节私钥原始值，
然后使用 secp256k1 派生压缩公钥和 `fbc1q...` 地址。
推导路径：`m/44'/9999'/0'/0/<index>`（9999 = FrostBit coin type）。

### 签名格式

节点要求：
- 公钥：33 字节 secp256k1 压缩公钥
- 签名：对 `protobuf.encode(AnyTx)` 字节的 SHA-256 哈希做 secp256k1 ECDSA 签名

### 节点通信

默认 Explorer 地址：`http://localhost:8080`，默认节点由 Explorer 的 `/api/nodes` 返回，可在设置页修改。
所有写操作（`/tx`）使用 `Content-Type: application/x-protobuf`，
查询操作（`/getaccount` 等）使用 JSON。

## DApp 接入示例

```javascript
// 等待钱包就绪
window.addEventListener('frostbit#initialized', async () => {
  // 请求连接；钱包会显示站点来源并要求用户批准。
  // 连接权限在钱包锁定时自动撤销。
  const [address] = await window.frostbit.requestAccounts();

  // 发送转账；每笔签名/发送请求都需要在钱包窗口中单独批准。
  await window.frostbit.sendTransaction({
    type: 'transaction',
    to: '0xReceiverAddress',
    tokenAddress: 'FB',
    amount: '100',
  });
});
```
