# 单机矿工加密钥匙库与人工解锁

这一模式让节点从加密的矿工身份文件启动。节点启动时人工输入口令，解锁后所有运行模块共用一个 `KeyManager`；不再把 hex 私钥保存在 runtime options、节点、共识或 FROST 结构体中。尚未拆分独立签名进程。

## 一条命令导入并启动

在原有节点启动参数中加入 `--keystore` 和 `--keystore-import-from`，就能完成首次加密、回读核对和启动。以下路径是示例；密文文件的父目录需要已存在，网络、身份、数据目录及 FROST 参数应沿用当前节点配置：

```bash
./bin/node \
  --config-dir /etc/dex/miner \
  --peers /etc/dex/miner/peers.json \
  --expected-address "$MINER_ADDRESS" \
  --data-dir "/var/lib/dex/$MINER_ADDRESS" \
  --resume=true \
  --keystore /etc/dex/miner.v3.json \
  --keystore-import-from /secure-import/miner.key \
  --no-simulator
```

- **首次运行，密文尚不存在**：读取原 hex 私钥，先核对已提供的预期矿工地址，再从真实终端无回显读取两次新口令；独占创建 0600 密文文件，回读解密并确认与原私钥一致，然后共用已经初始化的持钥对象继续启动。不需要单独执行 `inspect`、`address` 或再次输入解锁口令。
- **以后运行同一条命令，密文已存在**：只输入一次口令解锁。默认跳过导入源文件，明文源已移走也可以启动。
- **失败时**：密码确认不一致、预期地址不符、源无效时不创建密文。已有目标损坏、是目录或符号链接时启动失败，不回退导入、不覆盖目标；创建后回读或后续配置校验失败会保留密文与原源文件，修正配置后可重试。

`node` 会核对 manifest、公钥、身份及数据目录，再以原矿工身份启动监听。默认保留原明文。Linux 密文文件的权限由程序设为 0600。

首次导入需要两次标准 KDF（加密、回读各一次），后续启动只需一次解锁；均发生在启动阶段，不改变运行中的签名或数据库路径。两次 KDF 顺序执行，每次约 256MiB 工作集，Go GC 可能暂留前次分配，因此首次进程峰值可能高于 256MiB。

### 启动成功后清理明文（Linux）

在同一条 `node` 命令中加 `--delete-keystore-import-source`。程序确认密文能解锁且对应原私钥，等 TCP 和 QUIC 绑定、本机 TLS 握手和启动更新确认成功后，同步密文及其目录，再删除 `--keystore-import-from` 指定的明文文件。

启动失败会保留明文。修正问题后可用同一条命令重试。已有密文时先解锁，再核对尚在的明文源，节点就绪后才清理。源文件已经清理的重启只需正常解锁。

自动清理要求源文件是私有的普通文件且只有一个硬链接，源和密文路径由 root 或节点账户控制。文件或路径发生变化时保留源并记录原因。删除作用于指定文件的目录项，历史备份和快照中的副本由各自的保留策略管理。

## 单独导入与核对工具

构建工具：`go build -mod=vendor -o bin/node-key ./cmd/node-key`，或运行 `scripts/build.sh` / `scripts/build.ps1`。Windows 输出应使用 `.exe`。发布打包也包含 `node-key`；发布包的节点程序名为 `dex`。

以下命令在可信终端执行。路径是示例；导入源必须是已有的、权限受限的 64 字符 hex 私钥文件。

```bash
umask 077
./bin/node-key import \
  --private-key-file /secure-import/miner.key \
  --out /etc/dex/miner.v3.json
./bin/node-key inspect --keystore /etc/dex/miner.v3.json
./bin/node-key address --keystore /etc/dex/miner.v3.json
```

`import` 从真实终端无回显读取两次口令，新口令至少 12 字节，应使用随机长口令并在服务器外保存恢复凭据。工具不接收口令值的命令行参数或环境变量，不输出私钥。新文件使用 Geth V3 AES-128-CTR + scrypt (`N=262144,r=8,p=1,dklen=32`)，独占创建并同步到磁盘，拒绝覆盖已有目标。Linux 创建权限为 0600，逐层拒绝符号链接。生产读取同样只接受这组标准参数；弱 KDF 旧文件需要在可信环境另行转换。

`address` 解锁后输出本链 `fbc...` 地址，必须与当前矿工、公开 peers 清单及数据目录身份一致。`inspect` 不解锁，只显示格式/KDF 和 **Ethereum 格式地址**；该元数据地址不是本链身份。

导入不会删除或改写原明文。现有系统在确认地址一致、加密文件可重复解锁、原数据可启动及离线备份可恢复后，仍需迁移旧 `LoadCredential`、明文源和有关备份。删除当前文件不能抹除既有磁盘快照。仓库中没有自动执行上述线上迁移。

## 启动

在真实终端前台或保持终端的持久会话中启动，保留当前网络和 FROST 的全部配置参数，只替换矿工密钥来源：

```bash
./bin/node \
  --config-dir /etc/dex/miner \
  --peers /etc/dex/miner/peers.json \
  --expected-address "$MINER_ADDRESS" \
  --data-dir "/var/lib/dex/$MINER_ADDRESS" \
  --resume=true \
  --keystore /etc/dex/miner.v3.json \
  --no-simulator
```

重启后再次人工解锁。错误口令、损坏文件、过量 KDF、来源冲突或身份不匹配都会在网络监听前失败，不回退读取明文。不能同时传 `--private-key-fd` 或 Windows 本地矿工明文目录。独立套利身份仍使用原独立来源，不能复用矿工身份。

可由受控启动器通过 `--keystore-password-fd N` 传一次性口令（FD>=3），允许末尾一个换行；读取后关闭 FD 并清理可控缓冲。显式使用 FD 时，首次导入也只消费一次口令，不再从终端二次确认；新口令仍须达到 12 字节。该 FD 必须与矿工/套利/wrap FD 不同，且需要写入者关闭发送端以结束读取。不要把口令保存成服务器上的 env 文件或密码文件。

普通 `systemctl start` 没有调用者的控制终端，不能自动弹出口令输入。此版本未实现 systemd 交互桥接，不要直接给现有 unit 加 `--keystore` 后期待它能解锁。人工模式使用 `Restart=no`；无需任何无人值守自动重启。updater 仍可检查和准备更新，keystore 模式下不会主动停机，正常关机后可激活，下一次由操作者解锁。Bootstrap 在读取一次性 FD 前运行，保留显式 password FD 给同 PID exec。

## 内存与边界

- `KeyManager` 只长期持有一个非导出的 ECDSA 身份对象，各业务模块只调用签名、BLS、VRF、Envelope 和 ECIES 操作，不提供私钥导出。地址、签名格式、摘要和 FROST 流程保持兼容。
- 解锁输入、可控解析缓冲和派生标量在消费后尽快清理。Go/Geth/终端实现可能产生内部副本，不能承诺可靠的全内存擦除；这不是硬件不可导出密钥。
- Linux keystore 启动在读取口令前设置 `RLIMIT_CORE={0,0}`、`PR_SET_DUMPABLE=0`，失败即停止，作用于当前进程。完整 root 可以绕过这些限制并调用持钥程序；加密与收敛提高获取成本，不能保证 root 读不到运行密钥。
- 本轮没有 `mlockall` 或专用安全堆，未阻止 swap、休眠和运行内存快照。主机层禁 swap/`MemorySwapMax=0` 必须另行确认资源和生效情况，不能据此文档当作已部署。
- Windows 提供真实控制台解锁；加密文件路径有 reparse 检查，但没有 Linux 同等的逐层原子打开/ACL 保证，应由管理员配置文件 ACL。Linux 的 core/dumpable 保护不适用于 Windows。
- 本轮仅保护矿工身份。FROST share、wrap key、Guardian 与原有创世份额配置没有一起迁移加密。

## 性能验收口径

标准 scrypt 每次调用约需 256MiB 工作集，成本只发生于启动。本轮 Windows 单次样本中，一体化首次导入及回读约 708ms、后续解锁约 351ms；分别执行 2 次、1 次标准 KDF（机器相关，非 SLA）。累计分配约 512MiB、256MiB，累计分配不是 RSS 峰值。输入上限为 4KiB 明文源和 64KiB 密文文件，因此新增启动步骤的工作量有界；运行中不重复 KDF、解析密钥或走 IPC，新增 StateDB/KV 点读、批量读、前缀扫描和写入均为 0。

Linux 隔离单矿工空库实机验收中，首次完整启动约 4.53 秒、进程峰值 RSS 555MiB；原数据目录下后续启动约 3.62 秒、峰值 RSS 304MiB。两次都验证了终端无回显、HTTPS `/status` 成功和 SIGTERM 正常退出，第二次移走明文源后仍使用同一命令。这些完整启动数值包含固定等待与数据库初始化，且关闭了 FROST/见证，不代表 KDF 基准或实际大库的内存上限。

固定大小签名每次 O(1)，B 次签名 O(B)。同机旧/新 ECDSA 基准在 1/19/20/21/5000 次规模下分配量相同；5000 次约 100.7ms→98.3ms，未见复杂度退化。BLS 公钥只缓存公开值。该基准覆盖 local20 对应的签名规模与高交易批次，不代表整网出块性能保证；没有修改 StateView 的扫描/overlay/状态转换算法。
