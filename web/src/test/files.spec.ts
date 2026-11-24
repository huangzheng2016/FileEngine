import { afterEach, describe, expect, it, vi } from 'vitest'

const http = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('@/api/http', async (orig) => ({ ...(await orig<typeof import('@/api/http')>()), get: http.get }))

import { listFiles } from '@/api/files'

afterEach(() => vi.clearAllMocks())

describe('listFiles', () => {
  it('sends an empty parent_path for the scan root and drops it while searching', async () => {
    http.get.mockResolvedValue({ items: null, total: 0 })
    await listFiles({ session_id: '7', parent_path: '', page: 1, size: 50 })
    expect(http.get.mock.calls[0][1]).toMatchObject({ session_id: '7', parent_path: '' })
    await listFiles({ session_id: '7', parent_path: '照片', search: 'a', page: 1, size: 50 })
    expect(http.get.mock.calls[1][1].parent_path).toBeUndefined()
  })
})
