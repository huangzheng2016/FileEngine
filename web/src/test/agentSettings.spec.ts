import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { defineComponent, h } from 'vue'

const agent = {
  vision_max_images_per_batch: 4,
  vision_image_max_px: 1024,
  vision_max_image_bytes: 1048576,
  concurrency: 6,
  max_file_read_size: 4096,
  archive_entry_max_size: 1048576,
  max_retries: 3,
  rate_limit_max_retries: 8,
  retry_base_delay: 1,
  retry_max_delay: 30,
  max_step: 40,
  chat_max_step: 40,
  system_prompt: '',
  agent_timeout: 600,
  category_policy: '',
}

vi.mock('@/api/settings', () => ({
  getConfig: vi.fn(async () => ({ agent: { ...agent }, version: 3, default_category_policy: '' })),
  getPrompt: vi.fn(async () => ({ default_prompt: '默认提示词', version: 3 })),
  saveConfig: vi.fn(async (patch: object, version: number) => ({
    agent: { ...agent, ...patch },
    version: version + 1,
    default_category_policy: '',
  })),
  listConfigVersions: vi.fn(async () => ({ items: [], total: 0 })),
}))

const settings = await import('@/api/settings')
const { default: AgentSettings } = await import('@/pages/AgentSettings.vue')

describe('AgentSettings', () => {
  it('saves only the fields the user changed, against the loaded version', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/', component: defineComponent({ render: () => h(AgentSettings) }) }],
    })
    await router.push('/')
    const w = mount(defineComponent({ render: () => h('div', [h(AgentSettings)]) }), {
      global: { plugins: [router] },
      attachTo: document.body,
    })
    await flushPromises()

    await w.find('input[name="concurrency"]').setValue('5')
    const save = [...document.body.querySelectorAll('button')].find((b) => b.textContent?.trim() === '保存')
    save!.click()
    await flushPromises()

    expect(settings.saveConfig).toHaveBeenCalledWith({ concurrency: 5 }, 3)
    w.unmount()
  })
})
