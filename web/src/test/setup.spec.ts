import { describe, expect, it } from 'vitest'
import { MODEL_PRESETS, guessCaps, presetFor } from '@/lib/modelForm'
import { crumbs, parentPath, suggestName } from '@/lib/storage'
import { applyInputAttrs } from '@/lib/inputAttrs'

const preset = (id: string) => MODEL_PRESETS.find((p) => p.id === id)!

describe('storage folder browsing', () => {
  it('goes up within the browse root for every protocol', () => {
    expect(parentPath('smb', 'photos/2024', '')).toBe('photos')
    expect(parentPath('smb', 'photos', '')).toBe('')
    expect(parentPath('smb', '', '')).toBeNull()
    expect(parentPath('sftp', '/volume1/photos', '/')).toBe('/volume1')
    expect(parentPath('sftp', '/volume1', '/')).toBe('/')
    expect(parentPath('nfs', '/export/a/b', '/export/a')).toBe('/export/a')
    expect(parentPath('nfs', '/export/a', '/export/a')).toBeNull()
    expect(parentPath('local', '/srv/files/a', '', ['/srv/files'])).toBe('/srv/files')
    expect(parentPath('local', '/srv/files', '', ['/srv/files'])).toBe('')
    expect(parentPath('local', 'C:\\Users\\me', '', ['D:\\data'])).toBe('C:\\Users')
    expect(parentPath('local', 'C:\\Users', '', ['D:\\data'])).toBe('C:\\')
  })

  it('builds a breadcrumb that stops at a local root', () => {
    expect(crumbs('smb', 'photos/2024', '')).toEqual([
      { label: 'photos', path: 'photos' },
      { label: '2024', path: 'photos/2024' },
    ])
    expect(crumbs('local', '/srv/files/a', '', ['/srv/files'])).toEqual([
      { label: 'files', path: '/srv/files' },
      { label: 'a', path: '/srv/files/a' },
    ])
  })

  it('names a storage after the chosen folder and host', () => {
    expect(suggestName('smb', '192.168.1.10', 'photos')).toBe('photos（192.168.1.10）')
    expect(suggestName('sftp', 'nas', '/')).toBe('nas')
    expect(suggestName('local', '', '/srv/files/家庭照片')).toBe('家庭照片')
  })
})

describe('model presets', () => {
  it('recognises the preset of a saved configuration', () => {
    expect(presetFor('openai', 'https://api.deepseek.com/v1/').id).toBe('deepseek')
    expect(presetFor('openai', '').id).toBe('openai')
    expect(presetFor('claude', '').id).toBe('claude')
    expect(presetFor('openai', 'https://example.com/v1').id).toBe('custom')
  })

  it('guesses context, vision and thinking from the model name', () => {
    expect(guessCaps('gpt-5-mini', preset('openai'))).toEqual({ context: 400_000, vision: true, thinking: false })
    expect(guessCaps('deepseek-reasoner', preset('deepseek'))).toMatchObject({ vision: false, thinking: true })
    expect(guessCaps('qwen-vl-max', preset('dashscope'))).toMatchObject({ context: 128_000, vision: true })
    expect(guessCaps('llama3.1:8b', preset('ollama')).context).toBe(32_000)
    expect(guessCaps('something-new', preset('custom')).context).toBe(128_000)
  })

  it('does not send a temperature to Claude by default', () => {
    expect(preset('claude').temperature).toBe(0)
  })
})

describe('input attributes', () => {
  it('turns off capitalisation on text fields unless they choose otherwise', () => {
    const plain = document.createElement('input')
    applyInputAttrs(plain)
    expect(plain.getAttribute('autocapitalize')).toBe('off')
    expect(plain.getAttribute('autocorrect')).toBe('off')
    const own = document.createElement('textarea')
    own.setAttribute('autocapitalize', 'sentences')
    applyInputAttrs(own)
    expect(own.getAttribute('autocapitalize')).toBe('sentences')
    const box = document.createElement('input')
    box.type = 'checkbox'
    applyInputAttrs(box)
    expect(box.hasAttribute('autocapitalize')).toBe(false)
  })
})
