# FileEngine Web

FileEngine 的前端 SPA，Vue 3 + TypeScript + Vite，嵌入 Go 二进制分发。

## 技术栈

- Vue 3 `<script setup>` + TypeScript，Vite 构建（产物指纹保留默认开启）
- Vue Router 4（**hash 模式**，`createWebHashHistory`）
- Element Plus + danceinbox-style 设计系统（Catppuccin 调色板 + 玻璃拟态）
  - `src/styles/tokens.css`、`src/styles/element-plus-override.css` 为设计系统契约文件，原样维护
  - 共享组件类统一放 `src/styles/components.css`，页面 `<style scoped>` 里只用 `var(--*)` token
- vue-i18n 双语言（`src/i18n/locales/zh-CN.yaml` / `en.yaml`，两份键集合必须完全一致）
- axios 实例 `src/api/index.ts`，baseURL `/api/v1`
- Agent 运行实时事件：原生 `EventSource` 消费 `GET /api/v1/sessions/:id/events/stream`（SSE）
- assistant 消息渲染：markdown-it + DOMPurify（`src/components/MarkdownView.vue`）

## 常用命令

```bash
npm ci            # 安装依赖（CI / build-wails.sh 使用）
npm run dev       # 开发服务器，/api 代理到 http://localhost:8080（SSE 同源可用，无需 ws:true）
npm run build     # vue-tsc 类型门禁 + vite build → dist/
npm run preview   # 本地预览构建产物
```

## 与后端的集成

- 仓库根目录 `embed.go` 用 `//go:embed all:web/dist` 嵌入构建产物；改动后必须 `npm run build`
- `build-wails.sh` 走 `npm ci && npm run build` 链路
- 开发时先后端 `:8080`，再 `npm run dev`；API 与 SSE 都经 vite proxy 转发
