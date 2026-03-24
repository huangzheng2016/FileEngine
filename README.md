# FileEngine

AI 驱动的文件整理引擎。通过 LLM Agent 自动扫描、分析、标记远程文件系统中的文件，生成整理方案并执行。

## 架构

```
┌──────────────────────────────────────────────────┐
│                  单一二进制文件                      │
│  ┌─────────────┐  ┌───────────────────────────┐  │
│  │  Vue 3 SPA  │  │    Go 后端 (Gin)          │  │
│  │  (嵌入式)   │──│  API ─ Agent ─ Executor   │  │
│  └─────────────┘  │       │         │         │  │
│                   │    GORM DB   RemoteFS      │  │
│                   │  (SQLite/    (5 种协议)     │  │
│                   │   MySQL)                   │  │
│                   └───────────────────────────┘  │
└──────────────────────────────────────────────────┘
```

**两阶段设计：**
- **Phase 1（仅数据库）** — Agent 扫描文件、AI 标记目录描述、规划移动/复制操作，不触碰实际文件
- **Phase 2（执行）** — Executor 从数据库读取方案，在远程文件系统上执行实际操作

## 特性

- **多协议文件系统** — Local / SFTP / FTP / SMB / NFS，统一 `RemoteFS` 接口
- **多模型 LLM** — OpenAI / Claude / Ollama 等任何 OpenAI 兼容 API
- **自底向上 AI 标记** — Agent 从最深层目录开始处理，可向上探索父目录获取上下文
- **智能级联** — 标记一个目录自动标记所有子项，跳过冗余处理
- **并发处理** — 可配置的并行 Agent 工作线程数
- **文件预览** — 支持文本、代码、图片等常见格式在线预览
- **在线提示词编辑** — 通过 Web UI 自定义 Agent 系统提示词
- **中英双语** — 前端界面支持中文 / 英文切换
- **单二进制部署** — 前端通过 `go:embed` 嵌入，运行时零外部依赖

## 快速开始

### 环境要求

- Go 1.21+
- Node.js 18+

### 构建

```bash
# 构建前端
cd web && npm install && npm run build && cd ..

# 构建后端（包含嵌入的前端）
go build -o fileengine .
```

### 运行

```bash
# 使用当前目录的 config.yaml
./fileengine

# 指定配置文件路径
./fileengine /path/to/config.yaml
```

启动后访问 `http://localhost:8080`。

### 配置

创建 `config.yaml`：

```yaml
server:
  port: 8080
  host: 0.0.0.0

database:
  driver: sqlite        # sqlite 或 mysql
  dsn: fileengine.db    # sqlite 为文件路径，mysql 为 DSN

model:
  provider: openai      # openai / claude / ollama
  api_key: sk-xxx
  model: gpt-4o
  base_url: ""          # 自定义 API 地址（ollama 必填）
  temperature: 0.1
  max_tokens: 4096

agent:
  batch_size: 10
  concurrency: 1        # 并行 Agent 工作线程数
  max_file_read_size: 102400
  max_retries: 3
```

文件系统连接（协议、主机、凭据）通过 Web UI 的「文件系统」页面管理，不在 config.yaml 中配置。

## 使用流程

1. **添加文件系统** — 在「文件系统」页面添加远程文件系统连接
2. **创建扫描** — 在「任务监控」页面选择文件系统，创建扫描任务
3. **AI 打标** — 点击「打标」启动 AI 分析，Agent 自底向上处理每个目录：
   - 列出目录内容
   - 读取关键文件（README、配置文件等）
   - 向上探索父目录获取上下文
   - 生成描述并规划文件移动方案
4. **审查方案** — 点击「查看计划」查看 Agent 的整理方案（此时所有变更仅在数据库中）
5. **执行** — 确认无误后点击「执行」，在远程文件系统上执行实际的文件移动/复制

## 项目结构

```
FileEngine/
├── main.go                     # 入口
├── embed.go                    # go:embed 嵌入前端
├── config.yaml                 # 配置文件
├── internal/
│   ├── config/                 # YAML 配置 + 热更新
│   ├── db/                     # GORM 模型 + 数据访问层
│   ├── remotefs/               # 统一文件系统接口 + 5 种协议实现
│   ├── scanner/                # 递归目录扫描器
│   ├── model/                  # LLM 多供应商工厂
│   ├── agent/                  # Eino ReAct Agent + 8 个工具 + 工作线程池
│   ├── executor/               # Phase 2 执行引擎
│   └── api/                    # Gin HTTP 处理器
└── web/                        # Vue 3 + Element Plus + TypeScript
    └── src/views/              # 5 个页面：文件浏览、文件系统、任务监控、
                                #          Agent 日志、系统配置
```

## API

基础路径：`/api/v1`

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/sessions` | 创建扫描会话 |
| GET | `/sessions` | 列出会话 |
| GET | `/sessions/:id` | 会话详情 |
| DELETE | `/sessions/:id` | 删除会话 |
| POST | `/sessions/:id/rescan` | 重新扫描 |
| POST | `/sessions/:id/tag` | 启动 AI 打标 |
| POST | `/sessions/:id/tag/stop` | 停止打标 |
| POST | `/sessions/:id/execute` | 启动执行 |
| POST | `/sessions/:id/execute/stop` | 停止执行 |
| GET | `/sessions/:id/plans` | 查看执行计划 |
| GET | `/files` | 查询文件 |
| GET | `/files/tree` | 目录树 |
| GET | `/files/:id/content` | 读取文件内容（预览） |
| PATCH | `/files/:id` | 更新文件元数据 |
| CRUD | `/categories[/:id]` | 分类管理 |
| CRUD | `/filesystems[/:id]` | 文件系统管理 |
| POST | `/filesystems/test` | 测试文件系统连接 |
| GET/PUT | `/config` | 获取/更新配置 |
| POST | `/config/test-model` | 测试模型连接 |
| GET/PUT | `/prompt` | 获取/更新系统提示词 |
| GET | `/logs` | 查询 Agent 日志 |
| GET | `/logs/stream` | SSE 实时日志流 |

## Agent 工具

AI Agent 在 Phase 1 中可使用 8 个工具：

| 工具 | 说明 |
|------|------|
| `list_files` | 按父路径、类型、标记状态查询文件 |
| `get_file_info` | 获取文件/目录详细元数据 |
| `read_file` | 从远程文件系统读取文件内容（有大小限制） |
| `update_description` | 设置 AI 生成的描述 |
| `mark_tagged` | 标记目录为已处理（级联到所有子项） |
| `list_categories` | 列出用户定义的目标分类目录 |
| `plan_move` | 规划文件/目录移动（仅数据库） |
| `plan_copy` | 规划文件/目录复制（仅数据库） |

## 技术栈

**后端：** Go, Gin, GORM, Cloudwego Eino

**前端：** Vue 3, Element Plus, Vue Router, Vue i18n, Vite, TypeScript

**协议：** pkg/sftp, jlaffaye/ftp, go-smb2, go-nfs-client

## 许可证

MIT
