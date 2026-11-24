import { computed, reactive, ref, watch } from 'vue'
import { fmtLabel } from '@/base/format'
import { PROTOCOL } from '@/base/labels'
import { toast } from '@/base/toast'
import { useFormValidation } from '@/base/useFormValidation'
import { useWizard } from '@/base/useWizard'
import { createFilesystem, testFilesystem, updateFilesystem, type FilesystemConn } from '@/api/settings'
import type { Filesystem, Protocol } from '@/api/types'
import { useFolderBrowser } from '@/lib/useFolderBrowser'
import { PROTOCOLS, authOf, hasLogin, isRemote, suggestName, type AuthMethod } from '@/lib/storage'

const emptyForm = () => ({
  name: '',
  description: '',
  protocol: 'smb' as Protocol,
  base_path: '',
  host: '',
  port: PROTOCOLS.smb.port,
  username: '',
  auth: 'password' as AuthMethod,
  password: '',
  private_key: '',
  key_passphrase: '',
})

// State and actions of the storage dialog. New storage is a guided flow (type → connection → folder → name);
// editing an existing one is a flat form with the same fields and a folder browser. Connecting and browsing
// are one request: the connection test lists the folders at the path.
export function useStorageForm(editing: () => Filesystem | null) {
  const form = reactive(emptyForm())
  const nameTouched = ref(false)
  const saving = ref(false)
  // the fingerprint the user accepted in this dialog
  const trustedKey = ref('')
  const roots = ref<string[]>([])
  // the folder browser is open (editing only)
  const browsing = ref(false)
  // the export the user typed for NFS; browsing stays inside it
  const nfsExport = ref('')
  const hostKey = reactive({ value: '', changed: false })

  const meta = computed(() => PROTOCOLS[form.protocol])
  const remote = computed(() => isRemote(form.protocol))
  const login = computed(() => hasLogin(form.protocol))
  const isNfs = computed(() => form.protocol === 'nfs')
  const isSftp = computed(() => form.protocol === 'sftp')
  const keyAuth = computed(() => form.protocol === 'sftp' && form.auth === 'key')
  const connPort = () => (remote.value && form.protocol !== 'nfs' ? form.port : 0)

  const endpointChanged = computed(() => {
    const e = editing()
    return (
      !!e &&
      (e.protocol !== form.protocol ||
        e.host !== form.host.trim() ||
        e.port !== connPort() ||
        e.username !== form.username.trim())
    )
  })

  // ---- validation ----

  const connectionRules = () => ({
    host: remote.value && !form.host.trim() ? '请输入地址' : '',
    port:
      remote.value && form.protocol !== 'nfs' && (!Number.isInteger(form.port) || form.port < 1 || form.port > 65535)
        ? '端口需在 1 到 65535 之间'
        : '',
    export: form.protocol === 'nfs' && !form.base_path.trim().startsWith('/') ? '请输入以 / 开头的导出路径' : '',
    password:
      login.value && !keyAuth.value && editing()?.password_set && !form.password && endpointChanged.value
        ? '地址或用户名已变化，请重新输入密码'
        : '',
    private_key: keyAuth.value && !form.private_key.trim() && !editing()?.private_key_set ? '请粘贴私钥' : '',
  })
  const vc = useFormValidation(connectionRules)
  const vn = useFormValidation(() => ({
    name: form.name.trim() ? '' : '请输入名称',
    base_path: form.base_path.trim() ? '' : `请选择${meta.value.pathLabel}`,
  }))
  const err = (k: keyof ReturnType<typeof connectionRules>) => vc.error(k)

  // What goes to the server. A secret that is left out keeps the stored value, '' clears it, and password and
  // key are never both sent.
  function conn(basePath: string): FilesystemConn {
    const p = form.protocol
    const c: FilesystemConn = {
      protocol: p,
      base_path: basePath,
      host: remote.value ? form.host.trim() : '',
      port: connPort(),
      username: login.value ? form.username.trim() : '',
    }
    if (!login.value || keyAuth.value) c.password = ''
    else if (form.password) c.password = form.password
    if (keyAuth.value) {
      if (form.private_key.trim()) {
        c.private_key = form.private_key
        c.key_passphrase = form.key_passphrase
      } else if (form.key_passphrase) c.key_passphrase = form.key_passphrase
    } else if (p === 'sftp' || editing()?.private_key_set) {
      c.private_key = ''
      c.key_passphrase = ''
    }
    if (p === 'sftp' && trustedKey.value) c.host_key = trustedKey.value
    return c
  }

  // ---- connecting and browsing ----

  const browser = useFolderBrowser(
    (path, signal) => {
      hostKey.changed = false
      return testFilesystem({ ...conn(path), id: editing()?.id }, signal)
    },
    (r, path) => {
      hostKey.value = r.host_key ?? ''
      hostKey.changed = !!r.host_key_changed
      if (r.success && form.protocol === 'sftp' && r.host_key && !trustedKey.value && !editing()?.host_key) {
        trustedKey.value = r.host_key
      }
      if (r.success && form.protocol === 'local' && path === '') roots.value = r.dirs.map((d) => d.path)
    },
  )
  const probe = browser.state

  const exportOf = (p: string) => nfsExport.value || p.trim()
  const browseTop = computed(() => (form.protocol === 'nfs' ? exportOf(form.base_path) : meta.value.browseRoot))
  const topLabel = computed(() =>
    form.protocol === 'smb'
      ? '全部共享'
      : form.protocol === 'local'
        ? '允许的文件夹'
        : form.protocol === 'nfs'
          ? browseTop.value
          : '/',
  )
  const startPath = () => (form.protocol === 'nfs' ? form.base_path.trim() : meta.value.browseRoot)
  const canUseFolder = computed(() => probe.ok && !probe.loading && !!probe.path)
  const folderBlocked = computed(() =>
    probe.loading ? '正在读取' : !probe.path ? (form.protocol === 'smb' ? '先进入一个共享文件夹' : '先选择文件夹') : '',
  )
  const where = computed(() =>
    remote.value ? `${form.host.trim()}${connPort() ? `:${connPort()}` : ''}` : fmtLabel(PROTOCOL, 'local'),
  )

  async function trustNewKey() {
    trustedKey.value = hostKey.value
    await browser.open(probe.path)
  }

  // ---- wizard (new storage only) ----

  const wizard = useWizard([
    { key: 'type', label: '类型' },
    {
      key: 'conn',
      label: '连接',
      validate: async () => {
        if (!vc.validate()) return false
        if (form.protocol === 'nfs') nfsExport.value = form.base_path.trim().replace(/\/+$/, '') || '/'
        return browser.open(startPath())
      },
    },
    {
      key: 'dir',
      label: '文件夹',
      validate: () => {
        if (!canUseFolder.value) return false
        form.base_path = probe.path
        if (!nameTouched.value) form.name = suggestName(form.protocol, form.host.trim(), probe.path)
        return true
      },
    },
    { key: 'name', label: '完成' },
  ] as const)
  const step = computed(() => wizard.current.value.key)

  function selectProtocol(p: Protocol) {
    if (form.protocol === p) return
    form.protocol = p
    form.port = PROTOCOLS[p].port
    form.base_path = ''
    form.auth = 'password'
    probe.ok = false
  }

  // A local folder has nothing to fill in: go straight to choosing one.
  async function next() {
    const from = step.value
    if (!(await wizard.next())) return
    if (from === 'type' && form.protocol === 'local') await wizard.next()
  }

  // Going back to the connection step means its result is stale.
  watch(step, (s, old) => {
    if (s === 'conn' && old === 'dir') {
      browser.abort()
      probe.ok = false
      if (form.protocol === 'local') wizard.back()
    }
  })

  // Editing: a changed connection invalidates the last result.
  watch(
    () =>
      JSON.stringify([form.protocol, form.host, form.port, form.username, form.password, form.private_key, form.auth]),
    () => {
      if (editing()) {
        probe.ok = false
        probe.error = ''
        browsing.value = false
      }
    },
  )

  // ---- editing: test the connection, browse for a folder ----

  async function openBrowser(): Promise<boolean> {
    if (!vc.validate()) return false
    browsing.value = true
    const start = form.base_path.trim() || startPath()
    if (form.protocol === 'local' && !roots.value.length) await browser.open('')
    const ok = await browser.open(start)
    if (ok || start === startPath()) return ok
    return browser.open(startPath())
  }

  const closeBrowser = () => (browsing.value = false)
  const touchName = () => (nameTouched.value = true)

  function useFolder(path: string) {
    form.base_path = path
    browsing.value = false
  }

  async function readKeyFile(file: File | undefined) {
    if (!file) return
    if (file.size > 64 * 1024) {
      toast.error('这不像私钥文件', '私钥文件通常只有几 KB')
      return
    }
    form.private_key = (await file.text()).trim()
  }

  // ---- open / save ----

  function init() {
    const f = editing()
    Object.assign(
      form,
      f
        ? {
            ...emptyForm(),
            name: f.name,
            description: f.description,
            protocol: f.protocol,
            base_path: f.base_path,
            host: f.host,
            port: f.port || PROTOCOLS[f.protocol].port,
            username: f.username,
            auth: authOf(f),
          }
        : emptyForm(),
    )
    nameTouched.value = !!f
    trustedKey.value = ''
    roots.value = []
    nfsExport.value = f?.protocol === 'nfs' ? f.base_path : ''
    browsing.value = false
    Object.assign(hostKey, { value: '', changed: false })
    browser.reset()
    wizard.reset()
    vc.reset()
    vn.reset()
  }

  async function submit(): Promise<Filesystem | null> {
    const e = editing()
    if (e && !vc.validate()) return null
    if (!vn.validate() || saving.value) return null
    saving.value = true
    try {
      const body = { ...conn(form.base_path.trim()), name: form.name.trim(), description: form.description.trim() }
      const saved = e ? await updateFilesystem(e.id, body) : await createFilesystem(body)
      toast.success(e ? '已保存' : '已添加存储')
      return saved
    } catch (x) {
      toast.error(e ? '保存失败' : '添加失败', x)
      return null
    } finally {
      saving.value = false
    }
  }

  return {
    form,
    nameTouched,
    saving,
    trustedKey,
    roots,
    browsing,
    meta,
    remote,
    login,
    isNfs,
    isSftp,
    keyAuth,
    endpointChanged,
    err,
    vc,
    vn,
    conn,
    browser,
    probe,
    hostKey,
    browseTop,
    topLabel,
    canUseFolder,
    folderBlocked,
    where,
    trustNewKey,
    wizard,
    step,
    selectProtocol,
    next,
    openBrowser,
    closeBrowser,
    touchName,
    useFolder,
    readKeyFile,
    init,
    submit,
  }
}

export type StorageFormApi = ReturnType<typeof useStorageForm>
