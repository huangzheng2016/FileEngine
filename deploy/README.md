# FileEngine Docker 测试环境

持久化的本地测试环境：多阶段构建产出单容器（前端嵌入后端二进制），状态落在
`deploy/data/`（SQLite + config.yaml），容器重建不丢数据。

## 文件

| 文件 | 作用 | 入库 |
|------|------|------|
| `Dockerfile` | 三阶段构建（web → go → alpine 运行时） | ✅ |
| `docker-compose.yml` | 单服务编排，端口/并发等经 `.env` 注入 | ✅ |
| `.env.example` | 配置模板 | ✅ |
| `.env` | 真实密钥/地址（模型 key、NFS 地址） | ❌ gitignored |
| `data/` | 运行时状态（config.yaml、fileengine.db*） | ❌ gitignored |
| `bootstrap.sh` | 宿主机一次性 provisioning：创建文件系统 + 模型提供方（API 幂等，按名称跳过） | ✅ |
| `sse-watch.sh` | 宿主机订阅会话 SSE 事件流 | ✅ |

## 启动

```bash
cd deploy
cp .env.example .env      # 填入真实 FE_FS_* / FE_MODEL_*（密钥只进 .env）
docker compose up -d --build
./bootstrap.sh            # 等待 API、创建 NFS 文件系统与模型提供方并自测连通
```

打开 `http://localhost:${FE_PORT}`（默认 8080）。此后扫描会话、打标、审查都
在 Web UI 操作；`.env` 里的 `FE_AGENT_*` 只在**首次**生成 config.yaml 时生效，
之后改 Agent 参数用 UI 配置页（热更新，写回 `data/config.yaml`）。
`FE_NFS_SCAN_CONNS`（NFS 扫描连接池大小，默认 8）是进程环境变量，
改动后 `docker compose up -d` 重建容器生效；扫描进度与速率
（dirs/entries per min）每 15 秒打在容器日志里。

## 日常使用

```bash
docker compose up -d          # 再启动（数据保留在 ./data）
docker compose logs -f        # 后端日志
./sse-watch.sh <session_id> run1.sse.log   # 持续监控打标事件流（含 token 统计）
docker compose down           # 停止（数据保留）
rm -rf data && docker compose up -d --build && ./bootstrap.sh   # 完全重置
```

镜像代码更新后 `docker compose up -d --build` 重建即可，DB 不动。

## 网络注意（macOS 必看）

- macOS 上 Docker Desktop / OrbStack 的容器**无法直连宿主机所在 LAN 的设备**
  （VPN kill-switch / NAT 限制），表现为 NFS `dial tcp ...:111: connection
  timed out`。两种解法：
  1. `FE_FS_HOST` 填 NAS 的 **Tailscale IP**（100.x.x.x）——容器经宿主机
     tailscale0 到达 NAS，本仓库实测可用；
  2. 在能直连 LAN 的 Linux docker 主机上运行本环境。
- 容器只需出站访问：NFS 服务器的 111/9024/2049（mountd 端口以
  `rpcinfo -p <host>` 为准）与模型的 HTTPS 端点，无需任何特权或内核 NFS。
- NFS 导出路径写**完整路径**（如 `/fs/1000/nfs`）。后端会自动探测真实
  export（深路径导出如 fnOS 也支持），无需手工拆分 export/子目录。

## 安全约定

- 真实密钥只在 `.env`（gitignored，权限 600）；`bootstrap.sh` 在宿主机读取后
  直接调 API 写入 DB，密钥**不进容器环境变量**（`docker inspect` 看不到）。
- 提交前 `git status` 核实：`.env`、`data/`、`*.db*` 均不出现。

## 数据调研（环境全部保留）

- 效果评估与修复清单：见 `EVALUATION.md`
- DB 只读查询：`python3 -c "import sqlite3; c=sqlite3.connect('file:data/fileengine.db?mode=ro',uri=True); ..."`
- SSE 事件流：`./sse-watch.sh <session_id> run.sse.log`；实测原始流与 UI 验收截图在 `runs/`（gitignored）
- 重跑打标：UI 会话中心"打标"按钮（tagged/tagged_partial 均可，幂等只补未打标项）
