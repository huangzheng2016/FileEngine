<p align="center">
  <img src="build/brand/logo.svg" width="96" alt="FileEngine">
</p>

<h1 align="center">FileEngine</h1>

<p align="center">用 AI 整理 NAS 上的文件。说清楚想怎么整理，AI 给出方案，你确认后才动文件，随时可以撤销。</p>

<p align="center">
  <a href="https://github.com/huangzheng2016/FileEngine/releases/latest">下载</a> ·
  <a href="#快速开始">快速开始</a> ·
  <a href="docs/security.md">安全说明</a>
</p>

## 它能做什么

- **一句话说明要求**：比如「照片按年份归档，安装包按软件归类」，或者直接选一个预设。
- **先看结果再动手**：AI 只做决定，不碰文件。你可以对比整理前后的目录，确认或修改每一处。
- **整理得有条理**：项目目录整体移动，不会被拆散；同一软件的多个版本放在一起；拿不准的放进「待确认」，不会直接执行。
- **越用越懂你**：你改过的位置会被记住，下次整理同一个存储时沿用。
- **安全执行**：复制并校验通过后才删除原文件，从不覆盖已有文件；可以先执行一小部分试试，执行后一键撤销。
- **照片和重复文件**：读取拍摄时间和相机信息，按内容找出重复文件并单独放置。
- **连接你已有的存储**：SMB、SFTP、NFS、FTP 和本机目录；模型支持 OpenAI 兼容接口、Claude 和本地 Ollama。

## 下载

到 [最新版本](https://github.com/huangzheng2016/FileEngine/releases/latest) 下载：

| 我想 | 下载 |
|---|---|
| 在自己电脑上用（Windows） | `FileEngine-<版本>-windows-amd64-setup.exe`，双击安装 |
| 在自己电脑上用（Mac） | Apple 芯片选 `…-macos-apple-silicon.dmg`，Intel 选 `…-macos-intel.dmg`，拖进「应用程序」 |
| 在自己电脑上用（Linux） | `FileEngine-<版本>-linux-amd64.deb`，`sudo apt install ./文件名.deb` |
| 装在 NAS 或服务器上，用浏览器访问 | `FileEngine-Server-<版本>-<系统>-<架构>` 压缩包，或用 [Docker](deploy/README.md) |

## 快速开始

**桌面版**：安装后打开，按提示添加存储和模型，就可以新建第一个整理任务。

**服务端**：

```bash
./fileengine          # 第一次运行会在当前目录生成 config.yaml
```

打开 `http://127.0.0.1:8080`，用启动日志里打印的访问令牌登录。默认只能从本机访问；要让局域网里的其他设备访问，把 `config.yaml` 里的 `server.host` 改为 `0.0.0.0`。

要整理服务器本机上的目录，启动前用 `FE_LOCAL_ROOTS` 指定允许访问的目录，例如 `FE_LOCAL_ROOTS=/volume1/photos ./fileengine`。

## 整理一次的过程

1. **新建任务**：选存储和目录，写下整理要求，点「创建并整理」。
2. **等 AI 整理**：扫描完成后自动开始，进展实时显示。
3. **确认结果**：对比整理前后的目录；「待确认」的项目逐个确认或修改。
4. **执行**：选择复制或移动，可以先执行一部分。不满意就一键撤销。

在 Btrfs、ZFS 等支持写时复制的文件系统上，推荐用复制模式：几乎不占额外空间，原来的目录结构也还在。

## 常见问题

- **AI 会不会误删文件？** 不会。AI 只写下建议；文件只在你点「执行」后才移动，而且从不覆盖、可以撤销。
- **文件内容会发给模型吗？** 默认会读取少量文件内容帮助判断，新建任务时可以关闭；密钥、证书这类敏感文件从不读取。用本地 Ollama 时数据不出本机。

## 参与开发

需要 Go 1.27、Node.js 24 和 [Task](https://taskfile.dev)。`task check` 运行全部检查，`task build` 构建服务端，`task package:desktop` 生成本机的桌面安装包。架构见 [docs/architecture.md](docs/architecture.md)，协作约定见 [AGENTS.md](AGENTS.md)。

## 许可证

[MIT](LICENSE)
