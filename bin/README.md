**简体中文** | [English](README.en.md)

# FrostBit 内测手册

欢迎参与 FrostBit 的内测！本手册是给测试员的完整指南，覆盖项目背景、奖励规则、启动步骤、前端模块、注意事项和反馈方式。

---

## 目录

- [零、项目简介](#零项目简介)
- [一、内测奖励规则](#一内测奖励规则)
- [二、注意事项（读完再开始测）](#二注意事项读完再开始测)
- [三、发布包目录与启动](#三发布包目录与启动)
- [四、钱包连接与独立测试账户](#四钱包连接与独立测试账户)
- [五、前端功能模块](#五前端功能模块)
- [六、关键状态流转图](#六关键状态流转图)
- [七、常见问题](#七常见问题)
- [八、反馈模板（GitHub Issue 直接复用）](#八反馈模板github-issue-直接复用)

---

## 零、项目简介

**FrostBit** 是一个**完全去中心化**的链上交易所——**没有中心化运营方、没有中心化做市商、连跨链资产本身也不在任何中心化托管方手上**。你能做的事：

- **现货交易**：在链上买卖已注册代币，限价挂单、撤单、查看盘口和成交历史
- **永续合约**：多空双向、可选杠杆、追加/提取保证金、查看标记价与资金费率，到强平线自动强平
- **跨链充值与提现**：把 BTC / ETH / BNB / TRX 打进来当链上资产用，也能原路提回去——**资产由全网集体保管，没有任何一方能单独动你的币**
- **代币管理**：浏览、发币、查看持有者分布和交易对
- **浏览器钱包**：FrostBit Wallet 扩展里管账户、签交易、连 DApp
- **区块浏览器**：查区块、查交易、查地址，看全链实时交易流

> **当前阶段**：内测（仿真测试）。整套系统在你本机一台电脑上即可跑起来，不需要服务器、不需要真钱包、不需要真 BTC。

---

## 一、内测奖励规则

我们希望尽早发现 bug、设计缺陷、使用问题。参与方式极简：

### 1.1 怎么提 bug

1. 在 GitHub 本仓库 **Issues** 板块提交 issue
2. **标题**写清现象的一句话概括
3. **正文**至少包含：
   - 复现步骤（哪个模块、点了什么按钮、填了什么内容）
   - 期望结果 vs 实际结果
   - 你钱包里的地址（从 FrostBit Wallet 扩展复制即可，用于发奖励 token）
   - 如有可能，附上节点 / Explorer 服务最后 20 行输出，或浏览器 F12 控制台截图；先删除路径、IP、账户等身份信息

### 1.2 奖励怎么算

- 正式主网上线后，按**有效 issue 数量**向你提供的钱包地址发放项目 token
- "有效"标准：能复现、非重复提交、非明显误操作
- **质量加成**：附 PoC、精确定位到模块/交易类型、给出修复建议的 issue 会额外加权
- 严重程度加成：崩溃类 > 资产损失类 > 功能异常 > 体验问题

### 1.3 怎么拿到钱包地址

装好 FrostBit Wallet 扩展后，在扩展里**本地生成一个新账户**（设密码 → 生成助记词 → 备份好助记词 → 完成），账户详情页顶部显示的就是你的地址。**这个地址是你本地生成、私钥只在你本机，填 issue 的时候粘贴过来即可；主网上线时会按同一个地址发放奖励。**

> ⚠️ 不要把节点签名身份当作奖励或日常测试钱包。节点密钥必须由网络运营者在仓库外单独保管；奖励地址必须是**你自己在钱包里生成、别人拿不到私钥**的账户。

> 注：issue 提交不限于 bug，**改进建议、文档错误、UI 问题** 同样计入统计。

---

## 二、注意事项（读完再开始测）

### 2.1 全本地即可，不需真跨链

这是最容易被误解的一点：**整套 FrostBit 在你本机运行就够了**，不要去折腾真实 BTC 测试网、不要去找水龙头、不要去真花比特币。

| 模块 | 理论（主网上线后） | 内测怎么处理 |
| --- | --- | --- |
| 出块、共识、同步 | 多物理节点跨机房 | 本机 30 个进程模拟，完全等价 |
| 现货 / 永续下单撮合 | 链上实时撮合 | 完全一致，本地就是真链 |
| BTC / ETH 充值 | 见证人观察外链 tx 后投票上链 | **外链 tx hash 可随意填**，见证人本地投票流程照常走；你可以用它专门测"假 hash 能否被拒"、"重复 hash 能否去重"、"多轮投票" 等 |
| BTC / ETH 提现 | FROST 签名完成后由节点广播到外链 | 签名流程完整跑，广播部分为本地模拟；你能看到 FROST 多轮交互、矿工集合选举、超时重签等完整细节 |
| 钱包扩展 | 装在任意 Chrome / Edge | 加载 `wallet/` 目录即可，内测期间所有数据都只在本地 |

### 2.2 所有数据都可以随时清零

一次性本地网络可以从 0 重建：先停掉节点与 Explorer，只删除明确挂载的 data 目录，绝不能连同 secret 目录一起删除或复制。详见[第三章第 6 节](#6-从零开始一条新链可选)。

### 2.3 这是仿真原型，不是生产版

- 节点现在严格为一进程一身份；本地多节点 Compose 仍共享一台宿主机，不能代表真实的故障域隔离
- 性能数据仅供参考，不等于生产指标
- **测试重点：数额与对账是否符合预期**。每一笔操作都请自己手工算一遍，看链上数字对不对：
  - **下单 / 成交**：挂单后冻结数量对不对？成交后买卖双方余额变化是否等于 `成交数量 × 成交价`？部分成交的剩余挂单、手续费扣除、撤单后余额退还是否准确？
  - **永续合约**：开仓保证金、强平价计算、未实现盈亏、资金费率结算、追加/提取保证金后可用余额是否严格一致？
  - **跨链充值 / 提现**：见证通过后到账数量 = 申报数量 − 手续费？提现扣款、锁仓、广播状态切换的每一步数字是否对得上？
  - **代币总量**：发币、转账、销毁后总量守恒，所有持有者余额之和等于总供应量
  - **矿工奖励**：出块奖励是否按规则分配，总量是否和区块数对得上
- 建议提 issue 时写清**"我算的是 X，实际显示 Y"**，便于我们定位

### 2.4 端口和防火墙

- 单个节点默认使用 **6000**（共识），Explorer 默认使用 **8080**（Web）；**7000** 只在本机诊断时用于 pprof
- 多节点网络必须为每个身份部署独立进程、地址和 secret mount；生产环境不要暴露 debug API 或 pprof

---

## 三、发布包目录与启动

### 下载发布包

1. 打开发布者提供的匿名公开仓库，进入 **Releases** 页面
2. 选择与服务器架构一致的 Linux 包，例如 `dex-vX.Y-linux-amd64.zip` 或 `dex-vX.Y-linux-arm64.zip`
3. 把 zip 解压到受控目录（例如 `/opt/dex`），解压后会得到下面描述的结构
4. 后续所有步骤都以解压出来的这个 `dex` 目录为工作目录

> 💡 Windows 节点发布已安全停用，直到具备 named-pipe 或 inherited-handle secret launcher。Windows Explorer 如有需要应单独打包，不能与节点放在同一个安全承诺中。
>
> 💡 每次升级都解压到新的空目录，不要覆盖旧目录；数据与 secret 独立挂载，切换前先备份并验证配置。

### 发布包目录结构

```text
dex/
├── dex                   ← 单个链节点进程
├── explorer              ← Explorer 后端
├── miner-register        ← 一次性矿工登记工具
├── frostbit-node         ← 发布镜像快速启动命令
├── start-dex-node        ← 只从 secret 文件打开继承 FD 的 launcher
├── README.md           ← 本文件（简体中文）
├── README.en.md        ← English version
├── config/runtime/       ← 本发布包选定的公开运行配置
├── explorer_web/dist/    ← 前端静态资源
└── wallet/               ← FrostBit Wallet 浏览器扩展
```

节点内置自动升级，`--auto-update=true` 默认开启；加 `--auto-update=false` 可停止检查新版本，不需要独立升级程序或服务。升级源固定为本仓库的正式不可变 Release，缓存默认位于 `<data-dir>/.node-updates`，保留网络配置、数据库和密钥。需要可用的 GitHub CLI `gh release verify-asset`；只有确认原 systemd 服务会对退出码 75 自动重启时，节点才主动优雅退出应用更新。手动启动、Docker 和禁止自动重启的服务会先准备更新，在正常停机后提交，供下一次启动使用。

### 矿工节点快速启动（推荐）

快速路径直接使用发布页给出的 Docker 镜像及 sha256 digest，不需要源码、
Git 或 Go 工具链。先准备同一发布版本的五个公开配置文件、节点私钥，启用
FROST 时再准备独立的 wrap key，然后执行：

```sh
sudo ./frostbit-node up \
  --image REGISTRY.example/frostbit-node:vX.Y.Z@sha256:REPLACE_WITH_RELEASE_DIGEST \
  --network-dir "$PWD/config/runtime" \
  --private-key-file /secure-import/miner/private_key \
  --frost-wrap-key-file /secure-import/miner/frost_wrap_key \
  --reward-address REPLACE_WITH_FIXED_FBC_REWARD_ADDRESS \
  --explorer-url https://REPLACE_WITH_NETWORK_EXPLORER
```

命令会自动拉取固定镜像、预检配置、从私钥推导矿工地址、选择唯一公网
IPv4 并启动持久数据卷。NAT 或多公网 IP 主机需追加
`--advertise-endpoint PUBLIC_IPV4:6000`。同步完成并从另一台机器确认 TCP/UDP
可达、共同高度区块 ID 一致后，执行第二条命令：

```sh
sudo ./frostbit-node register --confirm-ready
```

下面的 `dex` / `start-dex-node` 用法保留给需要直接管理二进制进程的高级部署；
它不是快速开始的必经步骤。

### 1. 准备工作

- 保持发布目录完整；程序依赖同级的公开 `config/` 与 `explorer_web/dist/`
- 在发布目录之外为本节点准备只读 `private_key`；启用 FROST 时再准备独立的 `frost_wrap_key`。空文件和示例内容都会安全失败，不能提交到 Git
- 只把 secret 文件的读取权限授予节点服务账户，并禁用 core dump
- 如果压缩包根目录存在 `CONFIGURATION_REQUIRED`，这个包是刻意生成的
  **不可运行公开模板**，其中没有任何验证者身份。请生成同一 chain ID 下完整一致的
  网络配置，替换 `config/runtime/` 下全部五个文件，并在发布工具中使用
  `--config-mode=runnable` 验证通过后再启动。任何私钥都不能放进
  `config/runtime/`。
- **安装 FrostBit Wallet 浏览器扩展**：
  1. 在管理员工作站打开 Chrome / Edge，进入 `chrome://extensions`
  2. 打开右上角的**开发者模式**开关
  3. 点击**加载已解压的扩展程序**，选择发布包中的 `wallet` 目录
  4. 安装成功后浏览器工具栏会出现 FrostBit Wallet 图标

  > 每次解压新版本后要重新加载：进入 `chrome://extensions` 找到 FrostBit Wallet，点击刷新按钮即可。

### 2. 启动单个链节点

节点严格执行“一进程一身份”，不会从 `peers.json` 读取私钥。随包的 `start-dex-node` 只把 secret **路径**放在环境中，直接打开文件为 FD 3/4，再把 FD 编号传给节点；密钥值不会进入 argv 或环境变量。

```sh
export MINER_ADDRESS=replace-with-derived-miner-address
export DEX_NODE_BIN="$PWD/dex"
export DEX_PRIVATE_KEY_FILE="/secure/$MINER_ADDRESS/private_key"
export DEX_FROST_WRAP_KEY_FILE="/secure/$MINER_ADDRESS/frost_wrap_key"

# EXAMPLE endpoint only: replace node.invalid with this node's public endpoint.
./start-dex-node \
  --bootstrap-mode=genesis \
  --config-dir="$PWD/config/runtime" \
  --peers="$PWD/config/runtime/peers.json" \
  --expected-address="$MINER_ADDRESS" \
  --data-dir="/var/lib/dex/$MINER_ADDRESS" \
  --resume=true \
  --advertise-addr=node.invalid:6000 \
  --no-simulator
```

生产部署优先使用公开仓库中的 Docker/Kubernetes 模板或 `deploy/secure-node/` wrapper；每个身份使用独立进程和独立 secret mount。

### 3. 启动 Explorer 服务

另开一个终端；如果本机未运行节点，把 `--nodes` 与 `--sync-node` 改为受信网络节点：

```sh
./explorer \
  --config-dir="$PWD/config/runtime" \
  --peers="$PWD/config/runtime/peers.json" \
  --listen=127.0.0.1:8080 \
  --nodes=127.0.0.1:6000 \
  --sync-node=127.0.0.1:6000 \
  --web-dir="$PWD/explorer_web/dist" \
  --data-dir=/var/lib/dex/explorer
```

看到 `Explorer listening at http://127.0.0.1:8080` 即启动成功。

### 4. 打开前端

浏览器访问：**<http://127.0.0.1:8080>**

推荐使用 Chrome 或 Edge，打开后顶部右上角可以**中英文切换**。

### 5. 停止顺序

测试结束时，**先关浏览器服务，再关链节点**：

1. 向 Explorer 进程发送 `SIGTERM`，等待退出
2. 再向节点进程发送 `SIGTERM`，等待状态写入完成

> 强制杀死节点可能损坏正在写入的区块数据，应始终优雅退出。

### 6. 从零开始一条新链（可选）

想清空所有历史数据、从 0 高度重来：

1. 确认节点和 Explorer 进程都已停止
2. 仅删除明确挂载的节点 data 目录（不要删除 secret 目录）
3. 删除 Explorer data 目录
4. 重新按第 2、3 步启动

---

## 四、钱包连接与独立测试账户

凡涉及下单、充值、提现、发币等操作都要先连接钱包。钱包扩展安装见 [三.1](#1-准备工作)。

### 4.1 创建独立测试账户

节点不会生成或导出矿工私钥。`peers.json` 只包含公开身份与 bootstrap 路由；节点签名密钥由网络运营者通过仓库外的 secret 文件和继承 FD 注入，不能复制到浏览器钱包。

1. 点击工具栏 FrostBit Wallet 图标打开扩展
2. 选择 **创建账户**，设置独立的钱包密码
3. 按扩展提示离线备份助记词，不要粘贴到 issue、聊天或终端
4. 使用新账户地址向测试网络运营者申请测试余额

仅限一次性本地开发网络时，运营者可以使用自己生成且从未用于真实资产的测试身份；也不能把密钥放入发布包或 Git 仓库。

### 4.2 连接钱包

1. 浏览器打开 Explorer 页面（`http://127.0.0.1:8080`）
2. 点击右上角 **"连接钱包"**
3. 在扩展弹窗中选择账户并授权
4. 连接成功后顶部会显示当前地址和余额

如果在未连接状态下点下单按钮，页面会提示"请先连接钱包"。

> 💡 **提 issue 时用到的钱包地址**，就是扩展里任意一个账户的地址，推荐直接用你主要测试的那个。

---

## 五、前端功能模块

浏览器打开后，左侧导航栏共 **10 个模块**。以下按导航顺序说明。

### 5.1 节点监控（Monitor）

默认主页，观察整条链运行情况。

**看什么：**
- 左侧“可用节点池”列出当前公开 manifest 与 Explorer 配置中的节点，可单选或多选
- 选中的节点进入顶部控制中心，显示实时高度、已接受高度、延迟、FROST 任务数、pending 交易数、候选区块数
- 每个节点卡片有状态徽标：**在线 / 离线 / 警告 / 已暂停**
- 矿工节点额外显示**矿工状态**和活性状态
- "区块元数据"开关：展示最新区块的 miner / state root / 交易类型分布
- "Frost 指标"开关：显示内存、goroutine、FROST 任务数等运行时指标

**能做什么：**
- **加入挖矿 / 停止挖矿**：切换任意节点矿工身份，测动态加入/退出
- **暂停节点 / 恢复节点**：模拟掉线场景，观察其他节点追赶
- **快照同步开关**：新加入节点用快照还是从头同步
- **自动同步**：每 5 秒自动拉取所有选中节点状态
- 点击节点卡片可打开详情弹窗，查看最近日志和区块
- 点击"候选区块"数字弹出未最终化区块头列表

### 5.2 区块浏览器（Explorer）

手动查询链上数据。

**搜索框支持 4 种输入**：区块高度、区块哈希、交易哈希、账户地址。

**详情页：**
- **区块详情**：高度、哈希、前块哈希、状态指纹、出块矿工、交易列表、出块奖励
- **交易详情**：交易哈希、类型、发起方、具体载荷（按交易类型动态展示，如转账、下单、DKG 参与、门限签名等）
- **地址详情**：该地址余额、交易历史，可按类型过滤

下方还有 **最近区块** 滚动列表。

### 5.3 DEX 聚合池（DEX Pool）

测试现货和永续下单。两个子标签：

#### 现货（Spot）
- **交易对下拉**：支持搜索，可从已注册交易对中选，也可手动输入
- **盘口流动性**：实时买卖盘深度，可点击复制价格
- **历史成交**：最近撮合记录
- **限价委托**：连接钱包后输入价格、数量下买单 / 卖单
- **我的订单**：按未成交 / 已成交 / 全部过滤

#### 永续合约（Futures）
- 顶部指标：**标记价格、现货价格、指数价格、资金费率、保险基金**（实时）
- **当前仓位**：多空方向、持仓数量、入场价、强平价、未实现盈亏
- **保证金操作**：单仓位**追加 / 提取保证金**（带最大可提取额度提示）
- **限价委托** + **杠杆选择**
- **我的订单 / 订单簿 / 成交记录** 同现货

> 两个子模块都需先在右上角"连接钱包"。

### 5.4 提现管理（Withdraws）

展示 FROST 门限签名驱动的**跨链提现队列**。

- 列表显示每笔提现状态：排队 / 签名中 / 已签名 / 已广播 / 完成
- 点击任一提现打开详情弹窗，看完整的 **FROST 协议轮次**、参与签名的见证人、广播到外链的交易信息
- 用来验证：门限 t-of-n 是否正确触发、签名人选举是否均匀、超时是否重试

> 内测下"已广播"步骤是本地模拟，不会真正上 BTC 链——你能看到完整的协议轮次和签名数据，这是测试关键。

### 5.5 见证体系（Witness）

见证人对跨链事件的观察与投票流程。

- 待处理见证请求列表（区块高度、类型、见证状态）
- 每一条请求可展开，看哪些见证人已签、哪些没签
- 用来验证见证人离线、分歧、超时、搁置的行为

### 5.6 节点矿工（Miner）

矿工视角的节点列表，和"节点监控"互补：

- 只列出当前活跃矿工集合
- 显示**活性状态**、参与共识的高度、最近出块情况
- 多矿工切换、epoch 变更时确认矿工集正确更新

### 5.7 跨链操作（Bridge）

BTC / ETH / BNB / TRX 充值和提现入口。

**充值测试流程（本地模拟）：**
1. 连接钱包
2. 在"申请充值地址"面板点击生成一个一次性入金地址
3. **外链交易哈希**栏——内测直接填任意一个 64 位十六进制串即可（比如粘贴一个区块哈希）；我们要验证的是链上见证流程，不是真的打币
4. 填金额、精度（ERC20/TRC20 合约代币才需要）后提交
5. 切到 "见证体系" 观察见证人投票
6. 投票通过后余额会体现在"地址详情"中

**提现测试流程：**
1. 在"发起提现"表单填 Token 地址、目标地址、金额
2. 签名并提交
3. 切到 "提现管理" 查看签名进度
4. 签名完成会走到"已广播"——内测下这一步是本地模拟，不会真的打到 BTC 链

### 5.8 Frost 门限密钥（Frost DKG）

FROST DKG（Distributed Key Generation）会话时间线：

- 每次 DKG 会话的参与者、当前轮次、轮次用时
- 失败/重试次数
- 生成的公钥指纹
- 用于确认新一轮矿工变更后密钥是否如期重新生成

### 5.9 资产代币（Tokens）

Token 注册表浏览和查询：

- 列出所有已发行代币：符号、总量、发行方、精度
- 点击可查看该代币的持有者分布和已注册的交易对
- 验证发币、增发、销毁等交易在链上的效果

### 5.10 最近交易（Recent Txs）

全链最新交易的实时滚动视图：

- 按交易类型筛选（转账、下单、撤单、充值、提现、DKG、签名等）
- 点击任一交易弹出详情，无需跳到"区块浏览器"
- 适合压测或功能回归做"实时观测点"

---

## 六、关键状态流转图

下面四张图是测试过程中最容易出状态卡住的四条流水线。测试时可以对照着看：链上状态跳到了哪一格、下一步应该是什么、超时兜底走哪条边。

### 6.1 充值请求（RechargeRequest）

```mermaid
stateDiagram-v2
    [*] --> PENDING: 用户发起充值
    PENDING --> PENDING_SELECTION: 等未来块 hash 作为 beacon
    PENDING_SELECTION --> VOTING: 选出见证人
    VOTING --> CONSENSUS_PASS: 多数投 PASS
    VOTING --> CONSENSUS_FAIL: 多数投 FAIL
    VOTING --> SHELVED: 票数不足 / 见证人掉线
    CONSENSUS_PASS --> CHALLENGE_PERIOD: 进入公示期
    CHALLENGE_PERIOD --> FINALIZED: 公示期结束无挑战（上账）
    CHALLENGE_PERIOD --> CHALLENGED: 有人发起挑战
    CHALLENGED --> ARBITRATION: 进入仲裁
    ARBITRATION --> FINALIZED: 仲裁维持原判
    ARBITRATION --> REJECTED: 仲裁推翻原判
    ARBITRATION --> SHELVED: 仲裁票数不足
    CONSENSUS_FAIL --> REJECTED: 终结
    SHELVED --> PENDING_SELECTION: 扩大见证/仲裁池重试
    SHELVED --> REJECTED: MaxShelveWaitBlocks 超时兜底
    FINALIZED --> [*]
    REJECTED --> [*]
```

**测试要点**：`SHELVED → PENDING_SELECTION` 的重试、`SHELVED → REJECTED` 的超时兜底、挑战公示期的时长、`CHALLENGE_PERIOD → FINALIZED` 后余额是否正确上账。

### 6.2 挑战与仲裁（ChallengeRecord）

```mermaid
stateDiagram-v2
    [*] --> CHALLENGE_PENDING_SELECTION: 挑战交易上链
    CHALLENGE_PENDING_SELECTION --> CHALLENGE_ACTIVE: 选出仲裁者
    CHALLENGE_ACTIVE --> CHALLENGE_FINALIZED: 票数达标（80/70/60/50% 递降）
    CHALLENGE_ACTIVE --> CHALLENGE_SHELVED: 票数不足 / 仲裁者失联
    CHALLENGE_SHELVED --> CHALLENGE_PENDING_SELECTION: 扩大仲裁池重试
    CHALLENGE_SHELVED --> CHALLENGE_FINALIZED: MaxShelveWaitBlocks 超时按已投多数强制结算
    CHALLENGE_FINALIZED --> [*]
```

**测试要点**：挑战质押金额是否正确冻结、仲裁者重选是否走未来块 hash、超时兜底路径是否保守推断、结算后挑战者质押金退还或罚没是否对账。

### 6.3 见证人 / 矿工生命周期（WitnessStatus）

```mermaid
stateDiagram-v2
    [*] --> WITNESS_CANDIDATE: 注册账户
    WITNESS_CANDIDATE --> WITNESS_ACTIVE: WitnessStakeTx（质押）
    WITNESS_ACTIVE --> WITNESS_UNSTAKING: WitnessUnstakeTx（申请退出）
    WITNESS_UNSTAKING --> WITNESS_EXITED: WitnessClaimStakeTx<br/>需 Height ≥ UnstakeHeight + UnstakeLockBlocks
    WITNESS_ACTIVE --> WITNESS_SLASHED: 作恶 / 持续失联被罚没
    WITNESS_EXITED --> [*]
    WITNESS_SLASHED --> [*]
```

**测试要点**：
- `ACTIVE → UNSTAKING` 之后该见证人**立即不再被选入新任务**，但 `pending_tasks` 中的旧任务仍需处理完
- 锁定期（`UnstakeLockBlocks`，默认 20 块）未到前调 `WitnessClaimStakeTx` 应被拒
- `UNSTAKING` 中的 `witness_locked_balance` 在领回前不得动用
- `WITNESS_SLASHED` 路径观察罚没金额流向（保险基金 / 挑战者）

### 6.4 跨链提现（FROST Withdraw）

```mermaid
stateDiagram-v2
    [*] --> QUEUED: FrostWithdrawTx 上链
    QUEUED --> SIGNING: 调度器选中，分发 FROST 轮次
    SIGNING --> SIGNED: 门限签名凑齐
    SIGNED --> BROADCAST: 广播到外链（内测为本地模拟）
    BROADCAST --> [*]
    SIGNING --> QUEUED: 超时 / 参与人掉线 → 重排队
```

**测试要点**：FROST 轮次能否如期推进、签名人选举是否均匀、超时重签是否回退到 `QUEUED` 再次调度、`SIGNED → BROADCAST` 的交易哈希是否可核对。

---

## 七、常见问题

| 现象 | 排查步骤 |
| --- | --- |
| 浏览器打开 `127.0.0.1:8080` 白屏 | 确认 Explorer 服务仍在运行并监听 8080；检查服务日志后刷新页面 |
| 节点全部显示“离线” | 确认节点服务仍在运行，并在服务器上用 `ss -lntup` 检查 6000 端口；同时检查 manifest 路由和防火墙 |
| 提现一直卡在"签名中" | 切到 "Frost 门限密钥" 看是否有活跃 DKG 会话；矿工数不足 t 时签不出来 |
| 订单提交后盘口没变化 | 看"节点监控" pending 交易数是否在增长，确认交易是否进入某个节点的 txpool |
| 两次启动后数据看起来不对 | 按 [三.6](#6-从零开始一条新链可选) 清空 `data/` 和 `explorer_data/` 后重启 |
| 节点启动报端口被占 | 上一次没有优雅退出或另一个服务占用端口；用服务管理器查清进程后再处理，不要盲目强杀 |
| 前端按钮点击无反应 | 浏览器 F12 打开控制台看有无红色报错；截图反馈给开发 |
| 钱包扩展图标不出现 | `chrome://extensions` 确认是否已加载 `wallet/` 目录，"开发者模式"开关是否打开 |
| 充值填不了"外链交易哈希" | 内测期间随便填一段 64 位十六进制即可，不需要真实 BTC tx |

---

## 八、反馈模板（GitHub Issue 直接复用）

```markdown
### 现象
一句话概括（例：永续合约追加保证金后，UI 显示的未实现盈亏没更新）

### 复现步骤
1. 通过 FD launcher 启动一个测试节点，再启动 Explorer
2. 打开前端 → 连接钱包（账户 0）
3. 进入 DEX 聚合池 → 永续合约
4. 下一笔 BTC/USDT 10x 多单
5. 点击"追加保证金" 100 USDT

### 预期结果
未实现盈亏根据新保证金刷新

### 实际结果
数字未变，刷新页面后才更新

### 环境
- 节点数：30（默认）
- 是否清过数据：是 / 否
- 快照同步：开 / 关

### 我的钱包地址（奖励发放用）
fbc1q...xxxx

### 附件
- 节点服务最后 20 行脱敏输出（可选）
- 浏览器 F12 控制台截图（可选）
```

---

祝测试顺利，期待你的 issue！
