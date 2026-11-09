# DEX 不可信 VPS 安全部署

这套模板把矿工私钥和 FROST wrap key 保存在一台可信 Linux 服务器上。VPS 每次启动节点时，通过一把受限 SSH fetch key 在很短的人工放行窗口内读取两个秘密；DEX 验证后分别从 FD 3、FD 4 读取，VPS 不把它们写入配置、环境变量、命令行或命名临时文件。

目标是用较低运维成本降低磁盘快照、镜像、备份、`argv` 和长期凭据泄漏的风险。它不等于远程签名器：启动时两个秘密和解密后的 FROST share 仍会进入 VPS 进程内存。若服务商正在实时控制 VPS，它仍可能在放行和启动时抓取内存或管道。

## 目录

```text
deploy/secure-node/
├── trusted-server/
│   ├── install.sh                 # 安装受限 SSH 账户及工具
│   ├── dex-secretctl              # 注册、放行、撤销和受限取密的统一工具
│   ├── sshd-dex-secrets.conf      # dex-secrets 用户的 SSH 限制
│   └── authorized_keys            # PUBLIC EXAMPLE；地址绑定的受限条目格式
└── vps/
    ├── install.sh                 # 安装二进制、公开配置、fetch key 和服务
    ├── start-dex-secure           # 取密、校验并通过 FD 启动 DEX
    ├── dex-node-secure.service    # 默认不重启的 systemd 服务
    ├── secure-node.defaults       # PUBLIC EXAMPLE；仅含非秘密设置
    └── trusted-server-known-hosts.defaults # PUBLIC EXAMPLE；host key 格式
```

以下步骤假设两端都是较新的 Debian/Ubuntu 风格 Linux，trusted-server 运行 OpenSSH，VPS 运行 systemd。每台 VPS 的进程只运行一个节点身份，并使用不同的矿工私钥、wrap key 和 SSH fetch key。

所需基础工具：trusted-server 安装 `openssh-server`、`openssl` 和常见 coreutils；VPS 安装 `openssh-client`、`jq`、`bash`、coreutils 和 systemd。安装脚本只检查依赖，不会擅自执行包管理器。

## 0. 准备公开节点配置

安全部署使用的 `peers.json` 必须是版本化的公开清单，不能出现任何私钥字段。每条 `genesis_validators` 都必须有规范的 `address`、固定 `reward_address`、完整 `secp_public_key`、`bls_public_key` 和 `is_miner=true`；`bootstrap_endpoints` 只保存与验证者地址绑定的拨号入口。VPS、trusted-server secret 槽位、授权 marker、数据目录和节点启动校验统一使用同一个矿工 `fbc` 地址，不再由外部数字编号关联身份。

本模板面向同时运行 FROST participant 的矿工，要求 `frost_default.json` 中 `enabled=true`。非 FROST 节点不需要 wrap key，不能直接使用这套无条件注入两个 FD 的 wrapper。

仓库中的公开清单内容标记为 EXAMPLE，并不是可直接启动的生产网络。尤其是默认 FROST `defaultK=4` 时，公开创世矿工集合不能只有一个节点。

在可信构建机编译 Linux 二进制：

```bash
bash scripts/build.sh prod
```

将 `bin/node`、准备好的公开配置目录，以及下面生成的非秘密 host key 文件安全传到 VPS。

## 1. 安装 trusted-server

在 trusted-server 的仓库目录执行：

```bash
cd deploy/secure-node/trusted-server
sudo sh ./install.sh
```

安装器会：

- 创建只能通过公钥认证的 `dex-secrets` 账户；账户密码被设为一次性随机高熵值并立即丢弃；
- 安装 root 持有的 SSH 限制和 `authorized_keys`；
- 安装统一的 `/usr/local/sbin/dex-secretctl`；
- 建立 root 可写、`dex-secretctl fetch` 只读的 `/srv/dex-secrets`。

旧版使用 `node-N` 槽位时不能自动迁移，因为数字编号本身不能证明对应哪个私钥地址。新安装器会明确拒绝旧 secret 目录、grant 或 `authorized_keys` marker，并打印迁移提示，不会猜测映射。迁移时先停机并备份旧槽位，在可信环境从其中私钥推导 `fbc` 地址并与 `peers.json` 的 secp 公钥核对，再把目录改为 `/srv/dex-secrets/<矿工地址>`、删除旧授权/grant，最后用 `provision --authorize-only --miner-address <矿工地址>` 重新绑定 VPS fetch 公钥。任何一步无法核实时都保留旧数据并停止，不要按旧编号硬映射。

安装器把受管 `Match User dex-secrets` 块放在 `/etc/ssh/sshd_config` 文件末尾，避免发行版较早的 `Include` 让 Match 状态污染后续全局配置。它先对候选文件执行 `sshd -t` 和有效配置检查，成功后才替换并 reload SSH；任何失败都会恢复原配置。

确认 trusted-server 的 `/etc/ssh/sshrc` 不向 stdout 输出任何 banner；`dex-secretctl fetch` 的 stdout 必须只包含一个密钥 token。

## 2. 带外固定 trusted-server host key

不要在不可信 VPS 上直接运行一次未经核验的 `ssh-keyscan`。应从 trusted-server 控制台或其他可信渠道取得 `/etc/ssh/ssh_host_ed25519_key.pub`，核对指纹后制作 OpenSSH `known_hosts` 文件：

```text
trusted.example.com ssh-ed25519 REPLACE_WITH_VERIFIED_HOST_PUBLIC_KEY
```

该文件只能有这一条非注释记录，且只包含 host、`ssh-ed25519` 和公钥三个字段，不能把 `.pub` 文件末尾的注释一起复制进去。若 SSH 使用非 22 端口，首字段应写成 `[trusted.example.com]:2222`。传给 VPS 安装器的文件必须和 `--trusted-host`、`--trusted-port` 完全对应。

## 3. 安装一台 VPS

下面命令只安装服务，不启动，也不设置开机自动启动：

```bash
cd deploy/secure-node/vps
MINER_ADDRESS='replace-with-the-canonical-fbc-address-from-peers'
sudo sh ./install.sh \
  --miner-address "$MINER_ADDRESS" \
  --binary /secure-import/node \
  --config-dir /secure-import/prod-config \
  --trusted-host trusted.example.com \
  --trusted-port 22 \
  --known-hosts-file /secure-import/trusted-server-known-hosts \
  --advertise-addr miner-a.example.com:6000 \
  --bootstrap-mode genesis
```

late join 改为：

```text
--bootstrap-mode latejoin
```

latejoin 的拨号入口从同一份 `peers.json.bootstrap_endpoints` 读取，不接受另一份命令行列表覆盖信任清单。清单中的每个创世验证者都必须恰好有一个唯一 route；创世模式还要求 `MINER_ADDRESS` 是其中的创世验证者，latejoin 则允许本机地址尚未出现在创世清单中，但必须存在至少一个非本机的受信创世 bootstrap。新身份不能由远端生成，仍只由本机 FD 私钥推导并通过 `--expected-address` 核对。

安装器会在 `/var/lib/dex/ssh/<矿工 fbc 地址>.secret_fetch` 生成一把该 VPS 专用的 ed25519 fetch key。已经存在时绝不覆盖。命令末尾会打印 `.pub` 公钥；公钥可以复制到 trusted-server，私钥不能离开 VPS。旧版无地址或 `node-N` 命名的 fetch key，以及 `/etc/dex/node`、`/var/lib/dex/node` 这类未绑定地址的运行目录，都会被明确拒绝；必须先备份，在可信环境验证私钥对应地址，再手工迁到下列地址化路径。

安装后的固定路径：

```text
/opt/dex/bin/node
/etc/dex/miner/
/etc/dex/secure-node.env
/etc/dex/trusted-server-known-hosts
/var/lib/dex/miners/<矿工 fbc 地址>/
/var/lib/dex/ssh/<矿工 fbc 地址>.secret_fetch
/var/log/dex/<矿工 fbc 地址>.log
```

`/etc/dex/secure-node.env` 只允许保存 `DEX_MINER_ADDRESS`、地址绑定路径和服务器地址等非秘密设置，不能加入矿工私钥、wrap key 或 FROST share。wrapper 在取密前会验证 `peers.json` 的每个创世身份都有完整 `address`、`reward_address`、secp/BLS 公钥和矿工标志，并按启动模式验证 bootstrap route；创世模式还要求本机地址恰好出现一次。启动节点时始终传入 `--expected-address`，因此 latejoin 的新地址不依赖清单生成，错误 secret 槽位也会在开放网络前失败。

## 4. 在 trusted-server 注册节点

把 VPS 打印出的 `.pub` 公钥传到 trusted-server。新节点可在 trusted-server 直接生成独立 wrap key：

```bash
MINER_ADDRESS='replace-with-the-canonical-fbc-address-from-peers-or-local-key'
sudo dex-secretctl provision \
  --miner-address "$MINER_ADDRESS" \
  --vps-public-key "/secure-import/$MINER_ADDRESS-fetch.pub" \
  --private-key-file "/secure-import/$MINER_ADDRESS-private.hex" \
  --generate-wrap-key \
  --source-address 203.0.113.10
```

两个秘密文件的格式都是一行 64 个十六进制字符。不要把真实值写在命令参数或 shell history 里；命令只接收文件路径。

若 VPS 出口 IP 不固定，必须显式改用 `--allow-any-source`。这会失去 `authorized_keys from=` 的来源 IP 限制，不推荐作为默认值。

新生成的 wrap key 位于：

```text
/srv/dex-secrets/<矿工 fbc 地址>/wrap_key.hex
```

必须在开始 DKG 前把 trusted-server 的密钥目录做加密离线备份。安装器或 provision 重跑都不会覆盖已经存在的 signing secrets。

已有 FROST 数据迁移时不能生成新 wrap key，必须导入原来加密 share/reveal 的同一把 key：

```bash
sudo dex-secretctl provision \
  --miner-address "$MINER_ADDRESS" \
  --vps-public-key "/secure-import/$MINER_ADDRESS-fetch.pub" \
  --private-key-file "/secure-import/$MINER_ADDRESS-private.hex" \
  --wrap-key-file "/secure-import/$MINER_ADDRESS-existing-wrap.hex" \
  --source-address 203.0.113.10
```

VPS fetch key 丢失或轮换时，只替换授权，不接触两个 signing secrets：

```bash
sudo dex-secretctl provision \
  --authorize-only \
  --miner-address "$MINER_ADDRESS" \
  --vps-public-key "/secure-import/$MINER_ADDRESS-new-fetch.pub" \
  --source-address 203.0.113.10
```

## 5. 日常启动

每次启动或重启前，在 trusted-server 开放两分钟窗口：

```bash
sudo dex-secretctl allow "$MINER_ADDRESS" 120
```

然后在 VPS 启动：

```bash
sudo systemctl start dex-node-secure.service
sudo systemctl is-active dex-node-secure.service
sudo journalctl -u dex-node-secure.service -n 50 --no-pager
```

服务使用 `Restart=no`。窗口外启动、SSH host key 不匹配、SSH 请求失败、返回值不是 64 位 hex、配置仍含私钥或 DEX 初始化失败时，都会停止而不是回退到落盘密钥。DEX 启动阶段的致命错误也会返回非零退出码。

无需等窗口自然到期时可立即撤销：

```bash
sudo dex-secretctl revoke "$MINER_ADDRESS"
```

也可以在 trusted-server 配置普通运维 SSH 后，把放行和远程启动合成一条命令：

```bash
sudo dex-secretctl allow "$MINER_ADDRESS" 120 && \
ssh ops@miner-a.example.com sudo systemctl start dex-node-secure.service
```

不要把服务设为 `Restart=always`。trusted-server 或 VPS 重启后，保持停机，人工再次放行是这套低成本设计的重要安全边界。

## 权限与泄漏边界

- `/srv/dex-secrets/<规范 fbc 地址>` 为 `root:dex-secrets 0750`，文件为 `0640`；受限的 `dex-secretctl fetch` 可读但不可改。
- 每条 SSH 公钥被 forced command 固定到唯一矿工地址，只接受精确的 `private` 或 `wrap` 请求，并关闭 shell、PTY 和转发。
- managed `authorized_keys` 只允许本工具生成的地址绑定受限条目；不要手工加入普通登录 key，同一 fetch 公钥也不能绑定多个矿工地址。
- grant 位于 trusted-server 的 `/run`，TTL 只能是 10..600 秒；服务器重启后默认没有放行。
- 窗口内允许同一 VPS 重复读取。这是最简方案的已知折中；若 VPS 已被实时控制，攻击者可能与正常启动竞争。
- VPS fetch key 不是矿工签名私钥，但属于放行凭据，仍必须每节点独立并限制权限。
- trusted-server 的系统盘、备份和管理员权限仍属于信任边界，建议使用全盘加密、独立备份和最少管理员账户。
- 禁止生产节点启用 debug API、pprof、core dump或模拟器。systemd 模板已关闭 core dump，启动参数不包含这些调试开关。
- 需要让 VPS 从密码学上永远接触不到签名私钥或 share 时，应实现远程签名器/HSM；本目录的 FD 注入方案不能提供这个性质。

更底层的 DEX FD 格式、FROST 迁移和公开 `peers.json` 约束见 [../secure-node-startup.md](../secure-node-startup.md)。
