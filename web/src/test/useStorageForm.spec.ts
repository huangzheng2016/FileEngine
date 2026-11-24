import { nextTick } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { filesystem } from './fixtures'

vi.mock('@/api/settings', () => ({
  createFilesystem: vi.fn(async (b: object) => ({ id: '9', ...b })),
  updateFilesystem: vi.fn(async (_id: string, b: object) => ({ id: '4', ...b })),
  testFilesystem: vi.fn(),
}))

const settings = await import('@/api/settings')
const { useStorageForm } = await import('@/lib/useStorageForm')

const probe = vi.mocked(settings.testFilesystem)
const listing = (over: object = {}) =>
  ({
    success: true,
    error: '',
    detail: '',
    host_key: '',
    host_key_changed: false,
    dirs: [{ name: 'photos', path: 'photos' }],
    ...over,
  }) as never

beforeEach(() => {
  vi.clearAllMocks()
  probe.mockResolvedValue(listing())
})

function fresh(editing: ReturnType<typeof filesystem> | null = null) {
  const s = useStorageForm(() => editing)
  s.init()
  return s
}

describe('useStorageForm connection body', () => {
  it('sends a typed password and no key for a password login', () => {
    const s = fresh()
    Object.assign(s.form, { host: ' nas ', username: ' me ', password: 'pw' })
    expect(s.conn('photos')).toEqual({
      protocol: 'smb',
      base_path: 'photos',
      host: 'nas',
      port: 445,
      username: 'me',
      password: 'pw',
    })
  })

  it('leaves the stored password alone when nothing was typed', () => {
    const s = fresh(
      filesystem({ id: '4', protocol: 'smb', host: 'nas', port: 445, username: 'me', password_set: true }),
    )
    expect('password' in s.conn('photos')).toBe(false)
  })

  it('clears the key of a password login on SFTP and never sends both secrets', () => {
    const s = fresh()
    s.selectProtocol('sftp')
    Object.assign(s.form, { host: 'h', username: 'u', password: 'pw' })
    expect(s.conn('/')).toMatchObject({ password: 'pw', private_key: '', key_passphrase: '' })
    s.form.auth = 'key'
    s.form.private_key = 'KEY'
    s.form.key_passphrase = 'phrase'
    const c = s.conn('/')
    expect(c).toMatchObject({ password: '', private_key: 'KEY', key_passphrase: 'phrase' })
  })

  it('keeps a stored key when only the passphrase is retyped', () => {
    const s = fresh(
      filesystem({ id: '4', protocol: 'sftp', host: 'h', port: 22, username: 'u', private_key_set: true }),
    )
    expect(s.keyAuth.value).toBe(true)
    s.form.key_passphrase = 'new'
    const c = s.conn('/')
    expect(c).toMatchObject({ password: '', key_passphrase: 'new' })
    expect('private_key' in c).toBe(false)
  })

  it('clears a stored key when a non-SFTP storage no longer uses one', () => {
    const s = fresh(filesystem({ id: '4', protocol: 'smb', host: 'h', port: 445, private_key_set: true }))
    expect(s.conn('x')).toMatchObject({ private_key: '', key_passphrase: '' })
  })

  it('sends no address, port or login for a local folder, and no port for NFS', () => {
    const s = fresh()
    s.selectProtocol('local')
    Object.assign(s.form, { host: 'ignored', username: 'ignored' })
    expect(s.conn('/srv')).toMatchObject({ host: '', port: 0, username: '', password: '' })
    s.selectProtocol('nfs')
    s.form.host = 'nas'
    expect(s.conn('/export')).toMatchObject({ host: 'nas', port: 0 })
  })
})

describe('useStorageForm wizard', () => {
  it('walks type → connection → folder → name and saves what was chosen', async () => {
    const s = fresh()
    await s.next()
    expect(s.step.value).toBe('conn')
    await s.next()
    expect(s.step.value).toBe('conn')
    expect(s.err('host')).toBe('请输入地址')
    Object.assign(s.form, { host: '192.168.1.10', username: 'me', password: 'pw' })
    await s.next()
    expect(s.step.value).toBe('dir')
    expect(probe).toHaveBeenLastCalledWith(
      expect.objectContaining({ base_path: '', host: '192.168.1.10', password: 'pw' }),
      expect.anything(),
    )
    expect(s.canUseFolder.value).toBe(false)
    expect(s.folderBlocked.value).toBe('先进入一个共享文件夹')
    await s.browser.open('photos')
    expect(s.canUseFolder.value).toBe(true)
    await s.next()
    expect(s.step.value).toBe('name')
    expect(s.form).toMatchObject({ base_path: 'photos', name: 'photos（192.168.1.10）' })
    expect(await s.submit()).toMatchObject({ id: '9', base_path: 'photos', port: 445 })
  })

  it('skips the connection step for a local folder and returns past it', async () => {
    probe.mockResolvedValue(listing({ dirs: [{ name: 'files', path: '/srv/files' }] }))
    const s = fresh()
    s.selectProtocol('local')
    await s.next()
    expect(s.step.value).toBe('dir')
    expect(probe).toHaveBeenCalledTimes(1)
    expect(s.roots.value).toEqual(['/srv/files'])
    s.wizard.back()
    await Promise.resolve()
    expect(s.step.value).toBe('type')
  })

  it('pins the NFS export that was typed and browses inside it', async () => {
    const s = fresh()
    s.selectProtocol('nfs')
    await s.next()
    Object.assign(s.form, { host: 'nas', base_path: '/volume1/photos/' })
    await s.next()
    expect(s.step.value).toBe('dir')
    expect(probe).toHaveBeenLastCalledWith(
      expect.objectContaining({ protocol: 'nfs', base_path: '/volume1/photos/' }),
      expect.anything(),
    )
    expect(s.browseTop.value).toBe('/volume1/photos')
    expect(s.topLabel.value).toBe('/volume1/photos')
  })

  it('rejects an NFS export that is not absolute', async () => {
    const s = fresh()
    s.selectProtocol('nfs')
    await s.next()
    Object.assign(s.form, { host: 'nas', base_path: 'volume1' })
    await s.next()
    expect(s.step.value).toBe('conn')
    expect(s.err('export')).toBe('请输入以 / 开头的导出路径')
    expect(probe).not.toHaveBeenCalled()
  })

  it('invalidates the connection result when going back to the connection step', async () => {
    const s = fresh()
    await s.next()
    Object.assign(s.form, { host: 'h', password: 'pw' })
    await s.next()
    expect(s.probe.ok).toBe(true)
    s.wizard.back()
    await Promise.resolve()
    expect(s.step.value).toBe('conn')
    expect(s.probe.ok).toBe(false)
  })

  it('resets the port and credentials when the connection type changes', () => {
    const s = fresh()
    Object.assign(s.form, { auth: 'key', base_path: 'x' })
    s.selectProtocol('sftp')
    expect(s.form).toMatchObject({ protocol: 'sftp', port: 22, base_path: '', auth: 'password' })
  })
})

describe('useStorageForm server fingerprint', () => {
  const sftp = () => {
    const s = fresh()
    s.selectProtocol('sftp')
    Object.assign(s.form, { host: 'h', username: 'u', password: 'pw' })
    return s
  }

  it('trusts the first fingerprint it sees and sends it along', async () => {
    probe.mockResolvedValue(listing({ host_key: 'SHA256:a' }))
    const s = sftp()
    await s.browser.open('/')
    expect(s.trustedKey.value).toBe('SHA256:a')
    expect(s.conn('/')).toMatchObject({ host_key: 'SHA256:a' })
  })

  it('does not replace a fingerprint that is already stored', async () => {
    probe.mockResolvedValue(listing({ host_key: 'SHA256:other' }))
    const s = fresh(filesystem({ id: '4', protocol: 'sftp', host: 'h', port: 22, host_key: 'SHA256:a' }))
    await s.browser.open('/')
    expect(s.trustedKey.value).toBe('')
  })

  it('reports a changed fingerprint and probes again once it is trusted', async () => {
    probe.mockResolvedValueOnce(
      listing({ success: false, error: '指纹变了', host_key: 'SHA256:b', host_key_changed: true, dirs: [] }),
    )
    const s = sftp()
    expect(await s.browser.open('/data')).toBe(false)
    expect(s.hostKey).toMatchObject({ value: 'SHA256:b', changed: true })
    expect(s.trustedKey.value).toBe('')
    probe.mockResolvedValueOnce(listing({ host_key: 'SHA256:b' }))
    await s.trustNewKey()
    expect(probe).toHaveBeenLastCalledWith(
      expect.objectContaining({ base_path: '/data', host_key: 'SHA256:b' }),
      expect.anything(),
    )
    expect(s.probe.ok).toBe(true)
    expect(s.hostKey.changed).toBe(false)
  })
})

describe('useStorageForm editing', () => {
  const stored = () =>
    filesystem({
      id: '4',
      name: 'nas',
      protocol: 'smb',
      host: 'a',
      port: 445,
      username: 'me',
      base_path: 'photos',
      password_set: true,
    })

  it('asks for the password again when the address or user changed', async () => {
    const s = fresh(stored())
    expect(s.endpointChanged.value).toBe(false)
    expect(s.vc.validate()).toBe(true)
    s.form.host = 'b'
    expect(s.endpointChanged.value).toBe(true)
    expect(s.vc.validate()).toBe(false)
    expect(s.err('password')).toBe('地址或用户名已变化，请重新输入密码')
    s.form.password = 'new'
    expect(s.vc.validate()).toBe(true)
  })

  it('drops the last result when the connection is edited', async () => {
    const s = fresh(stored())
    // opening the dialog fills the form, which the watcher sees once
    await nextTick()
    expect(await s.openBrowser()).toBe(true)
    expect(s.probe.ok).toBe(true)
    expect(s.browsing.value).toBe(true)
    s.form.host = 'b'
    await Promise.resolve()
    expect(s.probe.ok).toBe(false)
    expect(s.browsing.value).toBe(false)
  })

  it('falls back to the browse root when the saved folder cannot be opened', async () => {
    probe.mockResolvedValueOnce(listing({ success: false, error: '不存在', dirs: [] }))
    const s = fresh(stored())
    expect(await s.openBrowser()).toBe(true)
    expect(probe.mock.calls.map((c) => c[0].base_path)).toEqual(['photos', ''])
  })

  it('saves through update and reports the saved storage', async () => {
    const s = fresh(stored())
    expect(await s.submit()).toMatchObject({ id: '4', name: 'nas' })
    expect(settings.updateFilesystem).toHaveBeenCalledWith('4', expect.objectContaining({ base_path: 'photos' }))
    expect(settings.createFilesystem).not.toHaveBeenCalled()
  })

  it('refuses a private key that is far too large to be one', async () => {
    const s = fresh()
    await s.readKeyFile(new File(['x'.repeat(70 * 1024)], 'big'))
    expect(s.form.private_key).toBe('')
    await s.readKeyFile(new File(['  KEY\n'], 'id_ed25519'))
    expect(s.form.private_key).toBe('KEY')
  })
})
