/// <reference types="vite/client" />

declare module '*.yaml' {
  const data: Record<string, any>
  export default data
}
