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
- **Phase 1（仅数据库）** — Agent 扫描文件、AI 标记目录描述、规划整理目标路径，不触碰实际文件
- **Phase 2（执行）** — Executor 从数据库读取方案，用户选择复制或移动模式，在远程文件系统上执行

## 特性

- **多协议文件系统** — Local / SFTP / FTP / SMB / NFS，统一 `RemoteFS` 接口
- **多模型 LLM** — 数据库管理多个 Model Provider，支持 OpenAI / Claude（原生）/ Ollama
- **原生 Claude 支持** — 通过 `eino-ext/components/model/claude` 直接调用 Anthropic API
- **每会话独立配置** — 每个扫描会话可选择不同的模型、是否允许读取文件、是否允许自动创建分类
- **自底向上 AI 标记** — Agent 从最深层目录开始处理，主动向上探索找到项目根目录并整体标记
- **智能级联** — 标记一个目录自动标记所有子项，跳过冗余处理
- **并发处理** — 可配置的并行 Agent 工作线程数，SQLite 单写连接避免锁冲突
- **文件预览** — 支持文本、代码、图片等常见格式在线预览
- **编辑抽屉** — 点击文件名直接编辑描述、目标路径等元数据
- **执行模式选择** — 执行时可选复制（保留原文件）或移动（删除原文件），支持目录递归操作
- **在线提示词编辑** — CodeMirror 编辑器 + One Dark 主题 + Markdown 高亮
- **实时日志** — SSE 实时流 + 轮次分页 + 内容截断展开
- **中英双语** — 前端界面支持中文 / 英文切换，状态标签全部 i18n
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

创建 `config.yaml`（仅服务器和数据库配置，模型通过 Web UI 管理）：

```yaml
server:
  port: 8080
  host: 0.0.0.0

database:
  driver: sqlite        # sqlite 或 mysql
  dsn: fileengine.db

model:                  # 全局默认模型（可选，推荐通过模型管理页面添加）
  provider: openai
  api_key: sk-xxx
  model: gpt-4o
  base_url: ""
  temperature: 0.1
  max_tokens: 4096

agent:
  batch_size: 10
  concurrency: 1
  max_file_read_size: 102400
  max_retries: 3
```

## 使用流程

1. **添加模型** — 在「模型管理」页面添加 LLM Provider（OpenAI / Claude / Ollama）
2. **添加文件系统** — 在「文件系统」页面添加远程文件系统连接
3. **创建扫描** — 在「任务监控」页面选择文件系统和模型，创建扫描任务
4. **配置会话** — 通过下拉菜单「会话设置」调整是否允许读取文件、自动创建分类
5. **AI 打标** — 点击「打标」，Agent 自底向上处理目录，主动向上探索找到项目根目录并整体标记
6. **审查方案** — 点击「查看计划」查看整理方案（所有变更仅在数据库中）
7. **执行** — 选择复制或移动模式，执行实际文件操作

## Agent 工具

| 工具 | 说明 | 条件 |
|------|------|------|
| `list_files` | 按父路径、类型、标记状态查询文件 | 始终可用 |
| `get_file_info` | 获取文件/目录详细元数据 | 始终可用 |
| `read_file` | 从远程文件系统读取文件内容 | `allow_read_file` 开启时 |
| `update_description` | 设置描述 | 始终可用 |
| `mark_tagged` | 标记目录为已处理（级联到所有子项） | 始终可用 |
| `list_categories` | 列出用户定义的目标分类目录 | 始终可用 |
| `set_target` | 设置整理目标路径（仅数据库） | 始终可用 |
| `create_category` | 创建新分类目录 | `allow_auto_category` 开启时 |

## 技术栈

**后端：** Go, Gin, GORM, Cloudwego Eino, eino-ext (OpenAI + Claude)

**前端：** Vue 3, Element Plus, Vue Router, Vue i18n, Vite, TypeScript, CodeMirror

**协议：** pkg/sftp, jlaffaye/ftp, go-smb2, go-nfs-client

## 许可证

MIT
