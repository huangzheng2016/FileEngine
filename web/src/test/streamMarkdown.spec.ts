import { describe, expect, it } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import StreamMarkdown from '@/components/StreamMarkdown.vue'

async function render(source: string, streaming: boolean) {
  const w = mount(StreamMarkdown, { props: { source, streaming }, attachTo: document.body })
  await flushPromises()
  await new Promise((r) => setTimeout(r, 50))
  await flushPromises()
  return w
}

describe('StreamMarkdown', () => {
  it('renders a partial stream with an unclosed code fence and a half table', async () => {
    const w = await render('## 结果\n\n| 分类 | 数量 |\n| --- |\n\n```json\n{"a": 1', true)
    expect(w.text()).toContain('结果')
    await w.setProps({ source: '## 结果\n\n| 分类 | 数量 |\n| --- | --- |\n| 照片 | 3 |\n\n```json\n{"a": 1}\n```\n' })
    await w.setProps({ streaming: false })
    await flushPromises()
    await new Promise((r) => setTimeout(r, 50))
    expect(w.text()).toContain('照片')
    w.unmount()
  })

  it('escapes raw HTML', async () => {
    const w = await render('前文 <script>alert(1)</script> <b>粗</b>', false)
    expect(w.find('script').exists()).toBe(false)
    expect(w.find('b').exists()).toBe(false)
    expect(w.text()).toContain('<script>')
    w.unmount()
  })

  it('opens external links in a new tab without opener access', async () => {
    const w = await render('[文档](https://example.com/x)', false)
    const a = w.find('a')
    expect(a.attributes('target')).toBe('_blank')
    expect(a.attributes('rel')).toContain('noopener')
    w.unmount()
  })
})
