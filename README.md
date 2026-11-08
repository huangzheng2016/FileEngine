# FileEngine

AI 驱动的文件整理引擎。通过 LLM Agent 扫描远程文件系统，生成整理方案并执行。

## 工作流程

```
扫描 ──▶ AI 标记（仅数据库）──▶ 审查方案 ──▶ 执行（复制/移动）
```

- **阶段一** — Agent 自底向上扫描目录，标记描述，设置目标路径。不触碰实际文件。
- **阶段二** — 用户选择复制或移动模式，执行器在文件系统上执行方案。

## 特性

- **5 种协议** — Local / SFTP / FTP / SMB / NFS，统一 `RemoteFS` 接口
- **多模型** — OpenAI / Claude（原生）/ Ollama，按会话独立配置
- **并发 Agent** — 可配置工作线程池，运行时可动态调整并发数
- **AI 指令** — 选中文件后用自然语言指示 Agent 操作（拆分、合并、重新分类等）
- **智能级联** — 标记目录自动级联到所有子项，外层 target 覆盖内层
- **目录过滤** — 扫描时支持黑名单/白名单过滤，可排除分类目录避免重复处理
- **分类权限** — Agent 创建的分类可被 Agent 修改，用户创建的默认受保护
- **自定义列** — 文件表列可拖拽排序、显示/隐藏，偏好持久化
- **Token 统计** — 全量统计所有 LLM 调用（含工具调用轮次）的 token 用量
- **单二进制** — Vue 3 前端通过 `go:embed` 嵌入，零运行时依赖
- **桌面版** — Wails v3 原生桌面应用，服务端在同一进程内运行（无子进程）
- **中英双语** — 前端界面支持中文 / 英文切换

## 快速开始

**环境要求：** Go 1.27（`go.mod` 固定 toolchain go1.27.2）、Node.js 24、[Task](https://taskfile.dev)（或用固定版本的 Wails CLI：`wails3 task <name>`）

```bash
# 服务端（静态单二进制，CGO_ENABLED=0，内嵌前端）
task build                        # → bin/fileengine
./bin/fileengine                  # 使用当前目录 config.yaml
./bin/fileengine /path/to/config.yaml

# 桌面版（Wails v3，服务端在进程内运行）
task desktop:build                # → bin/fileengine-desktop（-tags production）
task desktop:dev                  # 开发模式，直接读取 web/dist

# 质量门禁（与 CI 一致）
task check
```

服务端启动后访问 `http://localhost:8080`。桌面版的配置和 SQLite 数据库位于用户数据目录（`--data-dir` 或 `FE_DATA_DIR` 可覆盖）。Linux 构建桌面版需要 `libgtk-4-dev libwebkitgtk-6.0-dev`。

## 配置

```yaml
server:
  port: 8080
  host: 0.0.0.0

database:
  driver: sqlite    # sqlite | mysql
  dsn: fileengine.db

agent:
  batch_size: 10
  concurrency: 1
  max_file_read_size: 102400
  max_retries: 3
```

所有 Agent 设置（并发数、批量大小、重试次数、系统提示词）支持运行时热更新，无需重启。

模型和文件系统连接通过 Web UI 管理。

> **提示：** 推荐在 Btrfs / ZFS 等支持 COW（Copy-on-Write）的文件系统上使用复制模式整理文件。COW 文件系统的复制操作近乎零开销且不占用额外磁盘空间，既能保留原始文件结构作为回退，又能获得接近移动的性能。

## 技术栈

| 层级 | 技术 |
|------|------|
| 后端 | Go, Gin, GORM (纯 Go SQLite), Cloudwego Eino |
| 前端 | Vue 3, Element Plus, TypeScript, Vite |
| 桌面 | Wails v3 |
| 协议 | SFTP, FTP, SMB, NFS, Local |
| LLM | OpenAI, Claude（原生）, Ollama |

## 许可证

[MIT](LICENSE)
