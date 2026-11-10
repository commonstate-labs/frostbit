# 节点内置 GitHub 自动升级

升级逻辑是主节点中的后台 goroutine，`--auto-update=true` 默认开启，不安装独立升级程序或 systemd 服务。唯一发布来源固定为 `commonstate-labs/frostbit` 的正式、不可变 GitHub Release。

从旧发布源迁移时，先停机，再手工替换原启动入口为已验证的新版本。启动新版本时用 `--auto-update-dir` 指定全新的缓存目录，保留旧缓存供回退。旧缓存中的清单绑定旧仓库，复用该目录可能使新版本拒绝启动。如需回退，恢复旧入口和原缓存路径。

## 参数

```text
--auto-update=true                 默认检查并准备兼容升级
--auto-update=false                停止新版本检查，继续使用已确认的缓存版本
--auto-update-interval=10m          后台检查间隔，至少一分钟
--auto-update-dir=<专用目录>        可选，默认 <data-dir>/.node-updates
--auto-update-gh-path=/usr/bin/gh   GitHub CLI 的绝对路径
```

未指定 `--data-dir` 时，以公开的配置路径、端口、监听地址和预期节点地址生成实例键，缓存位于用户缓存目录 `dex/node-updates/<实例键>`。缓存与升级状态使用普通文件，不写 StateDB。目录必须属于当前节点用户，禁止其他用户写入或通过不安全软链访问。程序不会修改 `/opt` 的原始发布、网络配置或密钥，也不会申请 root 权限。

需要预装支持 `gh release verify-asset` 的官方 GitHub CLI。GitHub 证明验证和候选只读预检会使用短生命周期辅助命令，没有额外常驻进程；辅助命令不继承节点密钥 FD。如需 GitHub 令牌，只向原节点服务提供该仓库的读取令牌，绝不提供发布写令牌，令牌只进入 GitHub 请求及 gh 校验。下载或证明校验不可用时继续运行当前版本。

## 准备与切换

1. 节点运行期间检查正式 Release，验证清单和节点程序的 GitHub 发布证明、摘要、大小、链 ID、平台、协议与数据库兼容性。
2. 候选程序只写入本节点缓存，以当前非 root 用户执行 `--upgrade-info` 和 `--check-config` 预检；校验前不执行候选。
3. 查询当前 systemd 服务的退出重启策略。只有 `Restart=on-failure` 或 `always` 且退出码 75 未被排除时，节点才为更新主动退出。
4. 先取消后台任务并完成原 `launcher.Stop`、关闭数据库，再原子提交 pending，退出 75，由原服务重新提供密钥 FD。刷盘或 StateDB/runtime/query/archive 任一关闭失败均阻止提交 pending；剩余资源继续清理，重复停止仍返回首次失败。
5. 原入口程序在读取任何密钥 FD 前检查缓存，以 Linux `execve` 在同一个 PID 中替换为已验证程序，保留工作目录、argv、env 和未读取的密钥 FD。
6. 候选启动后，每秒检查真实监听、共识无同步暂停或安全停机、StateDB 最终化高度及区块 ID 与本地 accepted 一致、32 字节状态指纹可用；连续健康至少 10 秒且该窗口中 accepted 高度实际推进才确认。提交竞态或暂时性读取错误重置窗口并继续等待，3 分钟内未满足则退出。候选确认元数据错误也使进程退出；下次原入口启动回到已确认版本，最高版本记录不下降，失败候选被隔离。关闭新版本检查不豁免已启动候选的确认。

手动启动、Docker 和 `Restart=no` 等没有确认自动恢复能力的服务不会为更新自行退出。它们准备版本后继续运行，在用户正常停机、数据库关闭后提交，下一次外部启动才使用新版本。节点完全没有运行时不再检查更新，恢复依靠原进程管理方式。缓存挂载为 `noexec`、权限不足、版本为 dev/unknown 或缺少明确链身份时不会自动安装。

`--auto-update=false` 不清除已确认的版本选择，避免关闭检查后意外降级。手工安装了更新的入口版本时以手工版本为准。旧入口可能长期负责 Bootstrap，因此 schema 1 的关键启动记录格式必须保持兼容；损坏或未知记录失败关闭，不能静默改跑旧入口。

## 发布

管理员先在 GitHub 仓库开启 Immutable releases，然后在干净且已提交的工作区执行：

```powershell
go run -mod=readonly ./cmd/release --goos linux --goarch amd64 --env prod --version v1.2.3 --publish
```

发布机 `GITHUB_TOKEN` 需要 `Contents:write` 和查询不可变设置的 `Administration:read` 权限；节点读取令牌不需要这些写权限。发布工具绑定完整 commit 和真实版本，先创建草稿，上传完整 ZIP、裸节点程序及平台清单，再正式发布并确认不可变状态。失败保留草稿。发行包中没有额外 updater 二进制或安装器。

从公开仓库的干净 checkout 发布。本地 HEAD 须和官方仓库的 `main` 一致。Release 还附有从同一公开提交生成的 `frostbit-<version>-source.zip`，其中包含 Explorer 后端、钱包源码和共享依赖。独立的 `frostbit-wallet-<version>.zip` 使用同次构建的钱包扩展，解压后在浏览器里加载包含 `manifest.json` 的 `wallet/` 目录。

首版每个 Release 发布一个平台；缺少对应平台清单的节点保持当前版本。协议变更使用 `--upgrade-mode=coordinated`，沿用全网协调部署和确定激活高度，节点内升级器拒绝自主安装。自动错峰不等于全网批次协调，不能保证升级期间不断块。

## 验证与性能

下载、摘要验证及启动校验随制品字节数线性增长，流式缓冲有界。每实例只有一个准备任务；缓存仅保留当前、回退和单一候选，不增长历史版本。出块与交易执行热路径新增 StateDB/KV 点读、批读、扫描和写入均为零。候选确认每实例每秒至多两个 StateDB 元数据点读、一次内存分片指纹汇总与常数次共识状态读取，时间为 O(S)、额外空间 O(1)，S 为配置分片数；不遍历 peer 集合、不扫描账户或前缀、不写状态。local20 的一次健康采样合计 40 次元数据点读和 20 次内存指纹汇总。

测试覆盖发布来源、摘要、篡改、重放、关闭开关、pending 尝试/确认/回退、手工入口优先和退出策略；故障注入覆盖五个数据库关闭阶段、停止失败禁止激活、TCP 存活但链冻结、未持久化、身份不一致、同步/暂停、安全停机及读取错误。真实 StateDB 验收最终化身份，local20 采样计数约束读取工作量。Linux 子进程回退演练由 CI 运行；原 systemd 凭据重开、缓存 noexec 和真实 local20 高交易块资源仍需部署环境验收，交叉编译与模拟状态测试不能代替这些验证。
