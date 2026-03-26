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
- **Wails 桌面版** — 可选的原生桌面应用，内嵌后端进程
- **中英双语** — 前端界面支持中文 / 英文切换

## 快速开始

**环境要求：** Go 1.25+, Node.js 18+

```bash
# 构建
cd web && npm ci && npm run build && cd ..
go build -o fileengine .

# 运行
./fileengine                      # 使用当前目录 config.yaml
./fileengine /path/to/config.yaml
```

启动后访问 `http://localhost:8080`。

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
| 后端 | Go, Gin, GORM, Cloudwego Eino |
| 前端 | Vue 3, Element Plus, TypeScript, Vite |
| 桌面 | Wails v2 |
| 协议 | SFTP, FTP, SMB, NFS, Local |
| LLM | OpenAI, Claude（原生）, Ollama |

## 许可证

[MIT](LICENSE)
