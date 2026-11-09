# 本地 20 节点 Docker 网络模拟

这个配置用于在 Windows Docker Desktop / WSL2 上启动 20 个独立 DEX 节点容器。每个节点都有固定容器 IP，节点之间直接通过 Docker bridge 网络通信，服务端看到的 `RemoteAddr` 会是对端容器 IP，而不是 `127.0.0.1`。

## 资源配置

- 每个节点 CPU 上限：1 核，`cpus: "1.0"`。
- 每个节点内存上限：768MB，`mem_limit: 768m`。
- Go 运行时限制：`GOMAXPROCS=1`，`GOMEMLIMIT=640MiB`。
- 每个节点写入层限制：20GB，`storage_opt.size: "20G"`。
- 每个节点数据目录：容器内 `/data/node`，归属该容器独立 writable layer。
- `local20` 环境放宽了 PeerGate 陌生 IP/可信 IP 限速，用于 20 节点本地同时握手和 gossip 压测。

注意：Docker Desktop 当前资源池如果小于 20 个节点的峰值总量，容器仍然共享同一个 WSL2/Docker VM 资源池；这些限制是单容器上限，不是物理资源保证。当前配置按本机轻量模拟调整，20 个节点同时跑满约需要 20 核和 15GB 内存。当前配置没有使用 named volume，因为 Docker Desktop 的 named volume 在 Windows 上没有可靠的 per-volume quota；把 `/data/node` 放在容器 writable layer 中，20GB 限制才会覆盖节点数据。

## 网络与端口

- Docker 网络：`dex-local20-net`
- 子网：`198.18.0.0/24`
- 节点 IP：`198.18.0.11` 到 `198.18.0.30`
- 容器内节点端口：`6000/tcp` 和 `6000/udp`
- 宿主机节点端口：`6000` 到 `6019`

`local20` 会开启 `EnableDebugAPI`，但日志、FROST metrics、orderbook debug、资源和区块耗时接口都要求 Explorer service token；浏览器还必须先建立钱包 operator session。pprof 保持关闭，也没有监听端口。不要把测试环境配置直接复制到生产环境；生产安全启动方式见 [secure-node-startup.md](secure-node-startup.md)。

## 密钥前置条件

Compose 文件不包含、也不会生成节点私钥。先在仓库外准备一个仅当前用户可读的目录，目录下建立 `node00` 到 `node19` 二十个子目录；每个子目录放本节点的 `private_key`，启用 FROST 时另放独立的 `frost_wrap_key`。二十个身份必须与公开 `peers.json.genesis_validators` 一一对应，不能复用真实资产网络的密钥。

空文件或写着示例内容的文件只会让节点安全失败，不能作为可提交占位符。公开仓库只保存 manifest schema 和挂载路径；实际文件应由 `cmd/network-init` 或受控 secret provisioning 流程在仓库外生成。当前 PowerShell 会话中设置：

```powershell
$env:DEX_NODE_SECRET_ROOT = 'D:\frostbit-local20-secrets'
```

Docker Desktop 必须能读取该目录，但不要为了方便把它移动进仓库或放宽给其他用户。`.local/` 虽已被 Git 与 Docker build context 排除，仍只适合一次性本地网络，不能当作生产 secret manager。

节点与 Explorer 必须使用同一个 service token；浏览器管理员地址必须显式加入 allowlist。启动 compose 前设置：

```powershell
$env:DEX_EXPLORER_SERVICE_TOKEN = '<32 到 512 字符的随机值>'
$env:EXPLORER_SIMULATOR_OPERATOR_ADDRESSES = '<管理员钱包的 fbc1 地址>'
```

如果浏览器不是通过默认的 `http://localhost:8080` 访问，再把 `EXPLORER_PUBLIC_ORIGIN` 设置为实际的精确 Origin（协议、主机和端口都要一致）。service token 只在节点与 Explorer 进程之间使用，不得写入浏览器代码或 localStorage。

## 常用命令

```powershell
docker compose -f .\deploy\docker-compose.local20.yml build
docker compose -f .\deploy\docker-compose.local20.yml up -d
docker compose -f .\deploy\docker-compose.local20.yml ps
docker compose -f .\deploy\docker-compose.local20.yml logs -f node00
```

Explorer 随 `up -d` 一起启动，浏览器打开：

```text
http://localhost:8080
```

停止并保留容器数据：

```powershell
docker compose -f .\deploy\docker-compose.local20.yml stop
docker compose -f .\deploy\docker-compose.local20.yml start
```

删除容器并清空 20 个节点数据后重启：

```powershell
docker compose -f .\deploy\docker-compose.local20.yml down
docker compose -f .\deploy\docker-compose.local20.yml up -d
```

只启动前 3 个节点做连通性检查：

```powershell
docker compose -f .\deploy\docker-compose.local20.yml up -d node00 node01 node02
```
