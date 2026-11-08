import { toast } from './toast'

export async function writeClipboard(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    return false
  }
}

// For menu items and buttons without their own feedback: reports the result as a toast.
export async function copyText(text: string) {
  if (await writeClipboard(text)) toast.success('已复制')
  else toast.error('复制失败：浏览器未允许访问剪贴板')
}
