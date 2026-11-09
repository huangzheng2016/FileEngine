# FileEngine

用 AI 整理 NAS 上的文件。说清楚想怎么整理，FileEngine 扫描目录、给出整理方案，你确认后再复制或移动。

```
扫描 ──▶ 整理方案（可选）──▶ AI 整理 ──▶ 确认结果（前后对比）──▶ 执行 ──▶ 一键撤销
```

## 特性

- **按你的意图整理**：一句话写下目标，或从预设开始（照片按年月、文档按类型、安装包按版本……）；可先让 AI 起草方案并修改后再整理。
- **助手对话**：随时用自然语言纠正或追问；你的修改会被记住，后续整理沿用。
- **结果可预期**：整个项目文件夹整体移动，同一软件的多个版本归到一起；AI 拿不准的文件会请你确认，确认前不会执行。
- **规则与脚本**：规则一次处理成批同类文件，AI 只处理剩下的部分，又快又省。
- **安全执行**：复制校验通过才删除源文件，从不覆盖，可先试运行一小部分，执行后可一键撤销。
- **照片与重复文件**：读取拍摄时间和相机，按内容识别重复文件并单独归放。
- **存储**：SMB、SFTP、NFS、FTP、本机目录；模型：OpenAI 兼容接口、Claude、Ollama。
- **服务端与桌面版**：单个二进制内嵌前端；桌面版基于 Wails v3。

## 快速开始

需要 Go 1.27、Node.js 24 和 [Task](https://taskfile.dev)。

```bash
task build                       # → bin/fileengine
./bin/fileengine                 # 读取当前目录的 config.yaml，不存在时自动创建
```

打开 `http://127.0.0.1:8080`，用启动日志里打印的访问令牌登录，然后添加模型和存储、新建整理任务。

```bash
task desktop:build               # 桌面版 → bin/fileengine-desktop
task check                       # 与 CI 相同的检查
```

Docker：见 [`deploy/`](deploy/README.md)。

## 配置

```yaml
server:
  host: 127.0.0.1    # 对外提供服务时改为 0.0.0.0
  port: 8080
database:
  driver: sqlite     # sqlite | mysql | postgres
  dsn: fileengine.db
```

模型、存储和智能体参数在界面中设置，保存即生效。本机目录存储需设置 `FE_LOCAL_ROOTS`。访问令牌、加密密钥与备份见 [安全说明](docs/security.md)。

在 Btrfs、ZFS 等支持写时复制的文件系统上，推荐用复制模式整理：几乎不占额外空间，原始结构仍可保留。

## 文档

- [架构](docs/architecture.md)
- [安全](docs/security.md)
- [参与开发](AGENTS.md)

## 许可证

[MIT](LICENSE)
