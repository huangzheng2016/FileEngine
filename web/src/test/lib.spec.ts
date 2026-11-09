import { describe, expect, it } from 'vitest'
import type { PreviewItem, ScanSession } from '@/api/types'
import { contextError, effortError, effortOptions, fmtTokens, isContextPreset, parseTokens } from '@/lib/modelForm'
import { crumbs, parentOf, sortItems } from '@/lib/paths'
import { describeMatch, splitExts, targetError } from '@/lib/rules'
import { nextAction, stageOf } from '@/lib/sessionView'
import { scopeExts } from '@/data/presets'

describe('model form', () => {
  it('accepts preset and custom reasoning levels within the allowed charset', () => {
    expect(effortError('')).toBe('')
    expect(effortError('xhigh')).toBe('')
    expect(effortError('ultra_2')).toBe('')
    expect(effortError('高')).not.toBe('')
    expect(effortError('a'.repeat(33))).not.toBe('')
  })

  it('offers the typed level next to matching presets', () => {
    expect(effortOptions('').map((o) => o.value)).toEqual(['low', 'medium', 'high', 'max'])
    expect(effortOptions('hi').map((o) => o.value)).toEqual(['high', 'hi'])
    expect(effortOptions('max').map((o) => o.value)).toEqual(['max'])
    expect(effortOptions('高').map((o) => o.value)).toEqual(['high', 'max'])
    expect(effortOptions('').map((o) => o.label)).toEqual(['低', '中', '高', '最高'])
  })

  it('parses and formats context lengths in tokens', () => {
    expect(parseTokens('300K')).toBe(300_000)
    expect(parseTokens('1.5M')).toBe(1_500_000)
    expect(parseTokens('64000')).toBe(64_000)
    expect(parseTokens('abc')).toBe(0)
    expect(fmtTokens(2_000_000)).toBe('2M')
    expect(fmtTokens(128_000)).toBe('128K')
    expect(isContextPreset(200_000)).toBe(true)
    expect(isContextPreset(300_000)).toBe(false)
    expect(contextError(0)).not.toBe('')
    expect(contextError(300_000)).toBe('')
  })
})

describe('preview paths', () => {
  it('builds breadcrumbs for relative and rooted sides', () => {
    expect(crumbs('照片/2023', '整理前')).toEqual([
      { label: '整理前', path: '' },
      { label: '照片', path: '照片' },
      { label: '2023', path: '照片/2023' },
    ])
    expect(crumbs('/照片/2023', '整理后', true).map((c) => c.path)).toEqual(['', '/照片', '/照片/2023'])
    expect(parentOf('a/b/c')).toBe('a/b')
    expect(parentOf('a')).toBe('')
  })

  it('lists folders first', () => {
    const items = [
      { name: 'b.jpg', is_dir: false, status: 'moved' },
      { name: 'z', is_dir: true, status: 'partial' },
      { name: 'a', is_dir: true },
    ] as PreviewItem[]
    expect(sortItems(items).map((i) => i.name)).toEqual(['a', 'z', 'b.jpg'])
  })
})

describe('rules', () => {
  it('normalizes extensions and describes conditions in words', () => {
    expect(splitExts('.JPG, png，*.heic jpg')).toEqual(['jpg', 'png', 'heic'])
    expect(
      describeMatch({
        exts: 'jpg,png',
        name_regex: '',
        path_prefix: '下载',
        file_type: 'file',
        min_size: 0,
        max_size: 0,
      }),
    ).toBe('扩展名 jpg、png · 位于 /下载')
    expect(
      describeMatch({ exts: '', name_regex: '', path_prefix: '', file_type: 'file', min_size: 0, max_size: 0 }),
    ).toBe('所有未整理的条目')
  })

  it('rejects unknown template variables and parent segments', () => {
    expect(targetError('照片/{year}/{month}/{name}')).toBe('')
    expect(targetError('照片/{taken_year}/{camera}/{name}')).toBe('')
    expect(targetError('照片/{yaer}/{name}')).toContain('{yaer}')
    expect(targetError('../外面')).not.toBe('')
    expect(targetError('')).not.toBe('')
  })

  it('expands scope presets to extensions', () => {
    expect(scopeExts({ key: 'all', exts: [] })).toEqual([])
    expect(scopeExts({ key: 'custom', exts: ['psd'] })).toEqual(['psd'])
    expect(scopeExts({ key: 'image', exts: [] })).toContain('jpg')
  })
})

describe('session next step', () => {
  const base = { status: 'scanned', tagged_files: 0, planned_ops: 0, executed_ops: 0, review_count: 0 } as ScanSession
  it('offers exactly one next action for each state', () => {
    expect(nextAction({ ...base, status: 'scanning' }, undefined).kind).toBe('none')
    expect(nextAction({ ...base, status: 'error' }, undefined).kind).toBe('rescan')
    expect(nextAction(base, undefined)).toEqual({ kind: 'tag', label: '开始整理' })
    expect(nextAction(base, undefined, true)).toEqual({ kind: 'plan_tag', label: '按方案整理' })
    const tagged = { ...base, tagged_files: 9 }
    expect(nextAction(tagged, undefined)).toEqual({ kind: 'tag', label: '继续整理' })
    expect(stageOf(tagged, undefined)).toBe('no_moves')
    expect(stageOf(base, undefined)).toBe('ready')
    const planned = { ...tagged, planned_ops: 30 }
    expect(nextAction(planned, undefined)).toEqual({ kind: 'execute', label: '执行' })
    expect(nextAction({ ...planned, executed_ops: 20 }, undefined)).toEqual({
      kind: 'execute',
      label: '执行其余 10 项',
    })
    expect(nextAction({ ...planned, executed_ops: 25, review_count: 5 }, undefined).kind).toBe('review')
    expect(nextAction({ ...planned, executed_ops: 30 }, undefined).kind).toBe('none')
    const running = { phase: 'tagging' } as const
    expect(nextAction(planned, running).kind).toBe('none')
    expect(stageOf(planned, running)).toBe('tagging')
    expect(stageOf(planned, { phase: 'revert' })).toBe('executing')
    expect(stageOf(planned, undefined)).toBe('planned')
    expect(stageOf({ ...planned, executed_ops: 30 }, undefined)).toBe('done')
  })
})
