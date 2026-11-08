# FileEngine Web

Vue 3 + TypeScript SPA on the danceinbox-style design system, embedded into the Go binary from `web/dist`.

```bash
npm ci
npm run dev     # Vite dev server, /api proxied to http://127.0.0.1:8080
npm run check   # prettier + eslint + lint-ui + vitest + vue-tsc
npm run build   # vue-tsc + vite build -> dist/ (no source maps)
```

- `src/base/`, `src/styles/`, `public/fonts/`: copied verbatim from danceinbox-style (project-owned: `base/app.ts`, `base/labels.ts`; `base/icons.ts` carries extra Lucide icons).
- `src/api/`: one typed module per resource over `http.ts` (axios, `{error}` envelope mapped for `errorMessage`, 401 → login).
- `src/lib/`: pure logic (event reducer, rAF event buffer, paths, rules, model form) with tests in `src/test/`.
- `src/components/`: transcript, chat and Markdown (`StreamMarkdown` lazily loads markstream-vue).
- `src/pages/`: routes and their dialogs.
