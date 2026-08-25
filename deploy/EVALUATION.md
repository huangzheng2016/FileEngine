# FileEngine 测试环境与效果评估报告（M18）

日期：2026-08-26 · 分支：feat/test-environment（仅本地 commit，未 push）· 模型：kimi-k3-0813（OpenAI 兼容代理，1M 上下文）

## 1. 环境（业主可直接使用）

- 访问地址：**http://localhost:18090**（容器 `fileengine-test`，`deploy/` 下 `docker compose up -d` 随时拉起；数据全部保留在 `deploy/data/`）
- 预置内容：NFS 文件系统（100.100.255.x，经 Tailscale）、SMB 文件系统（同 NAS 双协议互验）、kimi-k3 模型提供方（temperature=1、reasoning_effort 可配）
- 查看数据：
  - SQLite DB（只读）：`python3 -c "import sqlite3; c=sqlite3.connect('file:deploy/data/fileengine.db?mode=ro',uri=True); ..."`，关键表 `file_entries / scan_sessions / categories / agent_logs`
  - 运行日志：`docker compose logs -f`
  - SSE 事件流：`./deploy/sse-watch.sh <session_id> [logfile]`；历史在 UI 会话中心或 `GET /api/v1/logs?session_id=N`
  - 四轮实测的 SSE 原始流与 UI 验收截图：`deploy/runs/`
- 重跑打标：会话中心选中会话 → 打标按钮（tagged/tagged_partial 均可重跑，只处理未打标余项）；或 `POST /api/v1/sessions/:id/tag`
- 本环境**从未也不会**调用任何 execute 端点（Phase 2 文件移动）；所有评估均为扫描+打标（纯 DB 操作）

## 2. 数据集与全链路

- 数据集：`nfs://…/fs/1000/nfs` 的 **ShareSpace** 子树，3046 条目（22 站点备份、7 个 MySQL 库备份、9 个项目压缩包、2789 个摄像头 MP4、Docker 配置等）；黑名单排除两个巨型源码树（oec_security / CTFPackage）
- 链路：NFS 深路径 export 自动探测挂载 → 扫描（约 2 分钟/3046 条）→ 打标 session（read_file/auto_category/script 开，vision 关）→ SSE 持续监控 → 报告/对账
- SMB 互验：同 NAS 共享 `ShareSpace`，小目录扫描条目数与 NFS 一致

## 3. 四轮打标实测对比（同一数据集）

| 轮 | 配置 | 耗时 | 吞吐 | token | 结果 |
|----|------|------|------|-------|------|
| A | c=1，默认 effort，batch 50 | 19.6min | ~155 文件/min | 639K（补跑后 883K） | tagged_partial（3 批 NFS XID 失败） |
| B | c=10，默认，batch 50 | 10.6min | ~286 文件/min | 912K | tagged（0 失败） |
| C | c=10，low effort，batch 50 | 3.8min | ~800 文件/min | 588K | tagged_partial（4 批死于重复建分类） |
| D | c=10，low，batch 200 + 全部新特性 | 4.1min | ~740 文件/min | 924K | **tagged**（0 失败，对账 文件0/目录10/软链0） |

结论：
1. **reasoning_effort=low ≈ 2.6× 吞吐**（800 vs 286 文件/min），completion token 约腰斩（48K→24K/轮）；实测代理真实生效（思考 token 924→151，-84%）。分类质量无可见退化（D 轮 42 项规划全部合理）
2. c=10 vs c=1 ≈ 1.8×（API 延迟瓶颈）；NFS 互斥锁下 10 并发零 XID 错误
3. token 结构：prompt 占绝对大头（约 97%），批次清单+分类摘要为主项；200/批与 50/批总 token 同量级，但调用次数更少

## 4. 分类质量评估（抽样）

- **归类合理性**：42 项规划全数合理——站点 tar.gz → 网站备份/按站点子路径；MySQL .sql.gz → 数据库备份/按库名；项目 zip → 代码项目（附包内摘要判断）；IDA 安装器 → 软件；clash 配置 → Docker配置；摄像头目录整体 → 监控录像；.DS_Store/空目录 → 未分类/自动忽略
- **描述质量**：具体且含证据（"含 14 个按日期命名的 .sql.gz（2026-05 至 2026-08）"）；容器目录只描述不打标符合设计
- **护栏行为**：重复建分类被硬拒并附现有分类参考（C 轮实测 4 次）；agent 被拒后改用现有分类；全程 7 个分类无膨胀、无空分类（run 结束自动清理机制兜底）
- **覆盖闭环**：失败批次三通道浮现（tagged_partial 状态/run_done 计数/报告失败批次节）；补跑幂等（只处理未打标余项，A 轮 3026→3036 后转 tagged）；对账行区分文件/目录/软链/已忽略垃圾

## 5. 修复清单（全部本地 commit，按提交序）

| commit | 修复 | 验证 |
|--------|------|------|
| 04c7c29 | NFS 深路径 export 自动探测（fnOS 整路径导出 MNT3ERR_ACCES）；NFS/FTP RPC 串行化（并发工具调用 XID 串线） | 真实服务器测试 + c=10 零 XID（B/D 轮） |
| f9a2e96 | owner-LAN 集成测试离网改 skip | go test 全绿 |
| c6eebd2 | update_description 列级写入（并行调用竞态抹掉 set_target——"监控录像空分类"真因） | 竞态交织回归测试 + B/D 轮摄像头目录正确归入 |
| 130af27 | ModelProvider.reasoning_effort 全链路（DB/API/factory/前端） | 代理实测 -84% 思考 token；C/D 轮 2.6× 吞吐 |
| c90688c | run_script 描述补文件字段+import 用法 | — |
| 3edb657 | deploy/ docker 测试环境全套 | 业主可直接 compose up |
| 673dffb | 垃圾文件默认过滤（.DS_Store/@eaDir/._* 等，忽略计入对账） | TestMarkJunkEntries + D 轮忽略 1 项 |
| d2e2182 | 压缩包预附目录摘要进批次 prompt | 单元测试 + D 轮 read_file 按需深读 11 次 |
| e82ef25 | 护栏拒绝改为 success=false 结果（eino 工具错误致命→可恢复） | C 轮 4 死批 → D 轮 0 死批 |
| 11c6699 | 空 AI 分类 run 结束自动清理+报告+prompt 纪律 | orchestrator 测试 |
| 66c15ca | run_script 增 mark_tagged 内建；batch_size 默认 200（上限 500） | 脚本测试 + D 轮 |
| 6918fe6 | tagged_partial 裸键修复+UI 可重跑 | 截图 01 |
| 70365ac | 移除仪表盘，首页会话中心 | 截图 01 |
| 02b89e0 | 表格操作列单排文字按钮+分类按钮组间距 | 截图 11/22 |
| 12bcc70 | AGENTS.md 凭据铁律+本地 commit 规则 | — |
| e02938f | SMB 双协议 .env.example 文档 | SMB 扫描互验 |
| 9562c84 | allow_vision 正式入参+新建扫描分组弹窗（联动/预设） | 截图 12/13 |
| f9ea4ea | 时间线批次导航+失败态+错误友好化+token 口径+时间戳统一 | 截图 25/26 |
| 347a71c | FileBrowser 整页重做（单条工具栏/统一导航树/分类抽屉/目录下钻） | 截图 08/09/11/23/24 |
| 3747b5b | 工具栏 1280 适配+列配置 v2（标记列默认可见） | 截图 08 |
| acae898 | 表格操作列透明叠字根治（弃 fixed=right）+common.copy 键+过场残留修复 | 截图 22 |
| 6c4be9e+b098583 | 步骤时间戳统一+i18n yes/no/copy 补键（YAML 布尔陷阱）+toast 去英文 | 构建绿 |

## 6. UI 验收（webbridge 真实浏览器，截图在 deploy/runs/ux-shots/）

- 会话中心：状态全本地化、token 服务端口径、批次导航、失败批红 chip+默认展开+技术详情折叠、时间戳本地 24h（01/16/17/25/26）
- 文件浏览：单条工具栏（1280/960/640 三档不崩）、目录/分类 Tab 统一树、分类管理抽屉无重叠、目录行可下钻、容器目录·不规划显式标注（08/09/11/23/24）
- 新建扫描：扫哪里/用哪个模型分组、高级选项折叠、视觉理解随模型联动禁用+提示（12/13）
- 模型管理：操作列单排文字按钮（测试连接/复制/编辑/删除）、API 列不再叠字（22）

## 7. 已知限制与后续

- **harness 连续模式**（散文件长 run 自拉批）：autotag 结构性改造，已报 tower 排期（UI 验收后），业主方向已记录
- 扫描大树无进度可见性（内存累积+末尾单事务写入）——已报 finding（scanner 增量落库+并发遍历建议）
- 配置页单位/危险警告、协议能力 tooltip（P2-11）等收尾项在 UX 队列
- Photos/MobileBackup（28.7K 条）可做更大规模回归；vision 尚未开启评估
