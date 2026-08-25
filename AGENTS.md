# AGENTS.md

FileEngine 项目协作规则（与 CLAUDE.md 互补；CLAUDE.md 管架构与代码风格，本文件管纪律）。

## 凭据卫生铁律

- 所有用户名/密码/API key/内网地址**绝不进仓库**——只放 gitignored 的本地文件（如 `deploy/.env`）。
- 仓库内只允许 `.env.example` 占位符；写文档举例一律用 `192.168.1.x`、`replace-with-real-key` 这类占位。
- 提交前必须 `git status` + `git diff --cached` 逐文件自查：无密钥、无内网地址、无运行时数据产物（`*.db*`、日志、`deploy/data/`、`deploy/runs/`）。

## Git 纪律（当前阶段）

- **只本地 commit，绝不 push**（无远程授权，业主明确要求）。
- 小步提交：每个可独立验证的修复立即单独 commit，不攒批；commit message 用 Conventional Commits（参照 git log）。
- 不 `git reset`/`rebase`/force 操作已提交历史。

## 测试环境纪律

- 持久环境在 `deploy/`（见 `deploy/README.md`）：`docker compose up -d --build` + `./bootstrap.sh`。
- 测试环境数据（容器/数据卷/SQLite DB/扫描与打标结果）**全部保留**，供业主随时调研，不清理。
- **绝不调用 execute 相关端点**（不移动/复制任何真实文件）；评估只用扫描+打标（Phase 1 纯 DB 操作）。
