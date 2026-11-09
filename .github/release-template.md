## 下载哪个文件

**桌面版**：装好就能用，双击打开。

| 系统 | 下载 | 安装 |
|---|---|---|
| Windows（x64） | `FileEngine-{{VERSION}}-windows-amd64-setup.exe` | 双击安装，不需要管理员权限 |
| Windows（ARM） | `FileEngine-{{VERSION}}-windows-arm64-setup.exe` | 同上 |
| macOS（Apple 芯片） | `FileEngine-{{VERSION}}-macos-apple-silicon.dmg` | 打开后拖进「应用程序」 |
| macOS（Intel） | `FileEngine-{{VERSION}}-macos-intel.dmg` | 同上 |
| Linux（x64） | `FileEngine-{{VERSION}}-linux-amd64.deb` | `sudo apt install ./FileEngine-{{VERSION}}-linux-amd64.deb` |
| Linux（ARM64） | `FileEngine-{{VERSION}}-linux-arm64.deb` | 同上，换成 arm64 |

macOS 首次打开如提示「无法验证开发者」：在「系统设置 → 隐私与安全性」中点「仍要打开」。

**服务端**：装在 NAS 或服务器上，用浏览器访问。下载 `FileEngine-Server-{{VERSION}}-<系统>-<架构>` 压缩包，解压后运行 `fileengine`，打开 `http://127.0.0.1:8080`，用启动日志里的访问令牌登录。Docker 部署见仓库的 `deploy/` 目录。

所有文件的校验值在 `SHA256SUMS` 中。
