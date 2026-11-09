# DEX 节点安全启动

生产默认关闭 debug API 和 pprof。矿工身份支持[单机加密钥匙库、启动人工解锁](keystore/README.md)，也保留原继承 FD 模式；两者在节点监听网络端口前加载身份，FROST wrap key 继续使用独立来源。公开配置、镜像、环境变量和命令行均不得保存密钥值。

## 外部 FD 模式

兼容参数仍可能存在于旧版本，但不属于生产部署接口：命令行参数会暴露在进程列表中，配置字段会进入磁盘、备份和镜像。部署模板不得使用它们，也不要同时传命令行密钥与 FD。

仓库已经提供可直接安装的 trusted-server 统一 `dex-secretctl`、VPS wrapper 和 systemd unit，见 [`deploy/secure-node/README.md`](secure-node/README.md)。下面是底层 FD 接口；实际部署不需要自己实现两个占位命令。

下面的两个占位命令必须各输出一个 token 后退出：矿工私钥为十六进制；wrap key 为恰好 32 字节、即 64 个十六进制字符，可带 `0x` 前缀。

```bash
MINER_ADDRESS='replace-with-the-canonical-fbc-address-from-peers'
./bin/node \
  --config-dir=/etc/dex/miner \
  --peers=/etc/dex/miner/peers.json \
  --expected-address="$MINER_ADDRESS" \
  --data-dir="/var/lib/dex/$MINER_ADDRESS" \
  --resume=true \
  --private-key-fd=3 \
  --frost-wrap-key-fd=4 \
  --no-simulator \
  3< <(trusted-private-key-command) \
  4< <(trusted-wrap-key-command)
```

约束如下：

- FD 模式仅支持 Unix-like 系统；每个进程只运行一个由该 FD 确定的节点身份。
- 两个 FD 必须不同且不小于 3。读取完成后 DEX 会立即关闭它们。
- `--expected-address` 必须使用该 VPS、secret 槽位和 `peers.json` 共同绑定的规范 `fbc` 矿工地址；私钥推导地址不一致时节点会在监听网络前失败。
- 持久数据目录也必须使用同一地址命名；重启固定传 `--resume=true`，新身份不得复用另一个地址的数据目录。
- 缺少严格的公开 `peers.json` 信任清单时启动会失败，不会生成 `miner_keys.txt` 作为后备。创世模式要求 FD 身份存在于 genesis_validators；latejoin 可使用未列入创世集合的新身份。清单不得包含任何真实私钥字段。
- 每台节点使用独立随机 wrap key，不能与矿工私钥相同或由其派生。

FROST FD 传入的是 wrap key，不是 share。share 仍由该 participant 生成，并以 AES-GCM 密文留在该节点数据库；外部模式只把非秘密的 key 指纹 marker 写入数据库。

公开清单把不可变的创世验证者信任根和可替换的 bootstrap 路由明确分开，不需要集中保存任何矿工私钥。以下内容是示例：

```json
{
  "_notice": "EXAMPLE: public identity only; no secrets",
  "schema_version": 1,
  "kind": "public-peer-manifest",
  "chain_id": 1,
  "genesis_validators": [{
    "address": "fbc1q...",
    "reward_address": "fbc1q...",
    "secp_public_key": "02...",
    "bls_public_key": "...",
    "is_miner": true
  }],
  "bootstrap_endpoints": [{
    "address": "fbc1q...",
    "endpoint": "miner-a.example:6000"
  }]
}
```

`address` 必须与 `secp_public_key` 匹配；每个创世记录都必须同时具备固定 `reward_address`、secp 与 BLS 公钥，并设置 `is_miner=true`。每个创世验证者还必须恰好绑定一个唯一 bootstrap endpoint，避免不同节点用不同路由写出不同创世状态。创世模式下，本机通过 FD 注入的私钥也必须与其中一个公开身份匹配，否则启动失败。latejoin 本机可以尚未列入创世集合，但 bootstrap 地址仍只取自清单，不再通过独立命令行列表覆盖。

## 已有 FROST 数据的迁移

已有 share/reveal 密文必须继续使用原来的 32-byte wrap key：

1. 停止节点，从可信的离线副本取得现有 `v1_local_frost_wrap_key` 的原始 32 字节值，在可信设备上编码成 64 位 hex；不要把值打印到终端日志或 shell history。
2. 用这把完全相同的 key 通过 `--frost-wrap-key-fd` 启动；不要临时生成一把新 key。
3. DEX 会先核对旧 key、marker，并解密验证所有现有 share/reveal 记录。全部成功后才写入外部 marker，并逻辑删除数据库中的旧 wrap key；不匹配时启动失败。

迁移成功后，外部 marker 会阻止节点退回数据库自动解锁；以后每次重启都必须再次通过 FD 提供同一把 wrap key。

删除 Pebble key 和执行 flush 不等于安全擦除。旧 SST、WAL、磁盘镜像、云快照或服务商已经复制的数据仍可能保留旧 wrap key；迁移只会让应用通过正常 KV 读取不到它，不能保证之后的块设备快照已清除旧字节。新部署应优先使用一开始就未保存 wrap key 的新数据卷。若旧运行环境可能被读取，应在新的独立控制域完成新 DKG/切换 epoch，而不是只更换 wrap key。不要恢复可能回滚 FROST nonce 状态的旧节点快照。

## Debug 与 pprof

以下开关在通用和生产配置中默认关闭；local20 会显式启用前两个：

```text
--enable-operator-control-api
--enable-debug-api
--enable-pprof
--pprof-bind=127.0.0.1
```

`--enable-operator-control-api` 只面向隔离测试网，开放模拟器控制、暂停和测试矿工切换。
`--enable-debug-api` 额外注册日志、FROST metrics 和 orderbook debug。两组节点入口都要求
`DEX_EXPLORER_SERVICE_TOKEN`，其值必须是 32 到 512 字符的单行随机值；缺失或过短会使节点
启动失败。浏览器还必须通过钱包签名建立 operator
session，不能直接持有该服务令牌。日志和负载数据仍属敏感信息，生产节点尤其是 FD 解锁后
的节点不要仅因接口有鉴权就默认开启。

pprof 是独立 listener，不会随 `--enable-debug-api` 开启，并且启用时必须保持 loopback
绑定。physical20 本地压测启用 token-protected operator control 与 diagnostics，但保持
pprof 关闭；生产 Kubernetes 清单同样不声明或暴露 pprof 端口。主机防火墙或集群
NetworkPolicy 仍应限制节点主端口来源。

## 安全边界

FD 注入主要阻止磁盘快照、镜像、argv 和环境变量中的被动窃取。节点运行期间，矿工私钥、wrap key 和解密后的 share 仍会进入普通进程内存；拥有宿主机或 hypervisor 控制权的攻击者仍可能定向抓取。因此应继续坚持每个 VPS 最多一个 FROST participant、不同底层控制域、最小资产热账户以及异常后停签和换 epoch。
