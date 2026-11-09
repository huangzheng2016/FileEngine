<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import Alert from '@/base/Alert.vue'
import BaseDialog from '@/base/BaseDialog.vue'
import CopyText from '@/base/CopyText.vue'
import Icon from '@/base/Icon.vue'
import Segmented from '@/base/Segmented.vue'
import Steps from '@/base/Steps.vue'
import { errorMessage } from '@/base/errors'
import { fmtLabel } from '@/base/format'
import { PROTOCOL } from '@/base/labels'
import { toast } from '@/base/toast'
import { useDirtyGuard } from '@/base/useDirtyGuard'
import { useFormValidation } from '@/base/useFormValidation'
import { useWizard } from '@/base/useWizard'
import { createFilesystem, testFilesystem, updateFilesystem, type FilesystemConn } from '@/api/settings'
import type { Filesystem, Protocol } from '@/api/types'
import { PROTOCOLS, PROTOCOL_ORDER, authOf, hasLogin, isRemote, suggestName, type AuthMethod } from '@/lib/storage'
import FolderPicker from './FolderPicker.vue'

// New storage is a guided flow (type → connection → folder → name); editing an
// existing one is a flat form with the same fields and a folder browser.
const props = defineProps<{ open: boolean; editing: Filesystem | null }>()
const emit = defineEmits<{ close: []; saved: [Filesystem] }>()

const empty = () => ({
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
const form = reactive(empty())
const nameTouched = ref(false)
const saving = ref(false)
const guard = useDirtyGuard(() => form)

const meta = computed(() => PROTOCOLS[form.protocol])
const remote = computed(() => isRemote(form.protocol))
const login = computed(() => hasLogin(form.protocol))
const isNfs = computed(() => form.protocol === 'nfs')
const isSftp = computed(() => form.protocol === 'sftp')
const keyAuth = computed(() => form.protocol === 'sftp' && form.auth === 'key')
const stored = computed(() => props.editing)
const endpointChanged = computed(() => {
  const e = stored.value
  return (
    !!e &&
    (e.protocol !== form.protocol ||
      e.host !== form.host.trim() ||
      e.port !== connPort() ||
      e.username !== form.username.trim())
  )
})

const connectionRules = () => ({
  host: remote.value && !form.host.trim() ? '请输入地址' : '',
  port:
    remote.value && form.protocol !== 'nfs' && (!Number.isInteger(form.port) || form.port < 1 || form.port > 65535)
      ? '端口需在 1 到 65535 之间'
      : '',
  export: form.protocol === 'nfs' && !form.base_path.trim().startsWith('/') ? '请输入以 / 开头的导出路径' : '',
  password:
    login.value && !keyAuth.value && stored.value?.password_set && !form.password && endpointChanged.value
      ? '地址或用户名已变化，请重新输入密码'
      : '',
  private_key: keyAuth.value && !form.private_key.trim() && !stored.value?.private_key_set ? '请粘贴私钥' : '',
})
const vc = useFormValidation(connectionRules)
const vn = useFormValidation(() => ({
  name: form.name.trim() ? '' : '请输入名称',
  base_path: form.base_path.trim() ? '' : `请选择${meta.value.pathLabel}`,
}))
type ConnKey = keyof ReturnType<typeof connectionRules>

const AUTH_OPTIONS = [
  { value: 'password' as const, label: '密码' },
  { value: 'key' as const, label: '私钥' },
]

function connPort() {
  return remote.value && form.protocol !== 'nfs' ? form.port : 0
}

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
  } else if (p === 'sftp' || stored.value?.private_key_set) {
    c.private_key = ''
    c.key_passphrase = ''
  }
  if (p === 'sftp' && trustedKey.value) c.host_key = trustedKey.value
  return c
}

// ---- folder browsing (also the connection test) ----

interface Probe {
  path: string
  dirs: { name: string; path: string }[]
  loading: boolean
  ok: boolean
  error: string
  detail: string
  hostKey: string
  hostKeyChanged: boolean
}
const probe = reactive<Probe>({
  path: '',
  dirs: [],
  loading: false,
  ok: false,
  error: '',
  detail: '',
  hostKey: '',
  hostKeyChanged: false,
})
// the fingerprint the user accepted in this dialog
const trustedKey = ref('')
const roots = ref<string[]>([])
const browsing = ref(false)
let probeCtl: AbortController | null = null

const browseTop = computed(() => (form.protocol === 'nfs' ? exportOf(form.base_path) : meta.value.browseRoot))
const topLabel = computed(() =>
  form.protocol === 'smb'
    ? '全部共享'
    : form.protocol === 'local'
      ? '允许的目录'
      : form.protocol === 'nfs'
        ? browseTop.value
        : '/',
)

// The export is what the user typed in the connection step; browsing stays
// inside it.
const nfsExport = ref('')
function exportOf(p: string) {
  return nfsExport.value || p.trim()
}

async function browse(path: string): Promise<boolean> {
  probeCtl?.abort()
  const ctl = (probeCtl = new AbortController())
  Object.assign(probe, { path, loading: true, error: '', detail: '', hostKeyChanged: false })
  try {
    const r = await testFilesystem({ ...conn(path), id: stored.value?.id }, ctl.signal)
    if (ctl.signal.aborted) return false
    Object.assign(probe, {
      dirs: r.dirs,
      ok: r.success,
      error: r.error ?? '',
      detail: r.detail ?? '',
      hostKey: r.host_key ?? '',
      hostKeyChanged: !!r.host_key_changed,
    })
    if (r.success && form.protocol === 'sftp' && r.host_key && !trustedKey.value && !stored.value?.host_key) {
      trustedKey.value = r.host_key
    }
    if (r.success && form.protocol === 'local' && path === '') roots.value = r.dirs.map((d) => d.path)
    return r.success
  } catch (e) {
    if (ctl.signal.aborted) return false
    Object.assign(probe, { ok: false, dirs: [], error: errorMessage(e), detail: '' })
    return false
  } finally {
    if (probeCtl === ctl) probe.loading = false
  }
}

async function trustNewKey() {
  trustedKey.value = probe.hostKey
  await browse(probe.path)
}

function startPath() {
  if (form.protocol === 'nfs') return form.base_path.trim()
  return meta.value.browseRoot
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
      return browse(startPath())
    },
  },
  {
    key: 'dir',
    label: '目录',
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
const canUseFolder = computed(() => probe.ok && !probe.loading && !!probe.path)

function pick(p: Protocol) {
  if (form.protocol !== p) {
    form.protocol = p
    form.port = PROTOCOLS[p].port
    form.base_path = ''
    form.auth = 'password'
  }
  probe.ok = false
  void wizard.next().then(() => {
    // nothing to fill in for a local folder: go straight to choosing one
    if (p === 'local') void wizard.next()
  })
}

// Going back to the connection step means its result is stale.
watch(step, (s, old) => {
  if (s === 'conn' && old === 'dir') {
    probeCtl?.abort()
    probe.ok = false
    if (form.protocol === 'local') wizard.back()
  }
})

// ---- open / reset ----

watch(
  () => props.open,
  (open) => {
    if (!open) return
    const f = props.editing
    Object.assign(
      form,
      f
        ? {
            ...empty(),
            name: f.name,
            description: f.description,
            protocol: f.protocol,
            base_path: f.base_path,
            host: f.host,
            port: f.port || PROTOCOLS[f.protocol].port,
            username: f.username,
            auth: authOf(f),
          }
        : empty(),
    )
    nameTouched.value = !!f
    trustedKey.value = ''
    roots.value = []
    nfsExport.value = f?.protocol === 'nfs' ? f.base_path : ''
    browsing.value = false
    Object.assign(probe, { path: '', dirs: [], ok: false, error: '', detail: '', hostKey: '', hostKeyChanged: false })
    wizard.reset()
    vc.reset()
    vn.reset()
    guard.reset()
  },
)

// Editing: a changed connection invalidates the last result.
watch(
  () =>
    JSON.stringify([form.protocol, form.host, form.port, form.username, form.password, form.private_key, form.auth]),
  () => {
    if (props.editing) {
      probe.ok = false
      probe.error = ''
      browsing.value = false
    }
  },
)

async function openBrowser() {
  if (!vc.validate()) return
  browsing.value = true
  const start = form.base_path.trim() || startPath()
  if (form.protocol === 'local' && !roots.value.length) await browse('')
  if (!(await browse(start)) && start !== startPath()) await browse(startPath())
}

function useFolder(path: string) {
  form.base_path = path
  browsing.value = false
}

const KEY_PLACEHOLDER = '-----BEGIN OPENSSH PRIVATE KEY-----\n…\n-----END OPENSSH PRIVATE KEY-----'

async function readKeyFile(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  if (file.size > 64 * 1024) {
    toast.error('这不像私钥文件', '私钥文件通常只有几 KB')
    return
  }
  form.private_key = (await file.text()).trim()
}

const err = (k: ConnKey) => vc.error(k)

// Enter in a field moves the flow forward.
function onSubmit() {
  if (props.editing || step.value === 'name') void submit()
  else if (step.value === 'conn') void wizard.next()
}

async function close() {
  probeCtl?.abort()
  if (await guard.confirmDiscard()) emit('close')
}

async function submit() {
  if (props.editing && !vc.validate()) return
  if (!vn.validate() || saving.value) return
  saving.value = true
  try {
    const body = { ...conn(form.base_path.trim()), name: form.name.trim(), description: form.description.trim() }
    const saved = props.editing ? await updateFilesystem(props.editing.id, body) : await createFilesystem(body)
    toast.success(props.editing ? '已保存' : '已添加存储')
    guard.reset()
    emit('saved', saved)
    emit('close')
  } catch (e) {
    toast.error(props.editing ? '保存失败' : '添加失败', e)
  } finally {
    saving.value = false
  }
}

const where = computed(() =>
  remote.value ? `${form.host.trim()}${connPort() ? `:${connPort()}` : ''}` : fmtLabel(PROTOCOL, 'local'),
)
const showConn = computed(() => !!props.editing || step.value === 'conn')
</script>

<template>
  <BaseDialog :open="open" :title="editing ? '编辑存储' : '添加存储'" width="640px" @close="close">
    <form id="storage-form" class="form" novalidate @submit.prevent="onSubmit">
      <Steps
        v-if="!editing"
        :steps="wizard.steps"
        :index="wizard.index.value"
        :reached="wizard.reached.value"
        @go="wizard.goTo"
      />

      <!-- 1. type -->
      <div v-if="!editing && step === 'type'" class="choices">
        <button
          v-for="p in PROTOCOL_ORDER"
          :key="p"
          type="button"
          class="choice"
          :class="{ chosen: form.protocol === p && wizard.reached.value > 0 }"
          @click="pick(p)"
        >
          <span class="choice-title">{{ PROTOCOLS[p].title }}</span>
          <span class="choice-desc">{{ PROTOCOLS[p].desc }}</span>
        </button>
      </div>

      <!-- name (editing) -->
      <div v-if="editing" class="form-grid" v-on="vn.form">
        <label class="field">
          <span class="field-label required">名称</span>
          <input v-model="form.name" name="name" maxlength="64" :aria-invalid="!!vn.error('name')" />
          <span v-if="vn.error('name')" class="field-error">{{ vn.error('name') }}</span>
        </label>
        <div class="field">
          <span class="field-label">连接方式</span>
          <span class="static-value">{{ PROTOCOLS[form.protocol].title }}</span>
        </div>
      </div>

      <!-- 2. connection -->
      <div v-if="showConn" class="form-grid" v-on="vc.form">
        <p v-if="!editing" class="span-2 text-muted text-sm step-intro">
          {{ PROTOCOLS[form.protocol].title }}：{{ PROTOCOLS[form.protocol].desc }}
        </p>
        <template v-if="remote">
          <label class="field" :class="{ 'span-2': isNfs }">
            <span class="field-label required">地址</span>
            <input
              v-model="form.host"
              name="host"
              class="mono"
              placeholder="192.168.1.10 或 nas.local"
              autocomplete="off"
              autocapitalize="off"
              spellcheck="false"
              :aria-invalid="!!err('host')"
            />
            <span v-if="err('host')" class="field-error">{{ err('host') }}</span>
          </label>
          <label v-if="form.protocol !== 'nfs'" class="field">
            <span class="field-label">端口</span>
            <input
              v-model.number="form.port"
              name="port"
              type="number"
              min="1"
              max="65535"
              :aria-invalid="!!err('port')"
            />
            <span v-if="err('port')" class="field-error">{{ err('port') }}</span>
            <span v-else class="field-hint"
              >{{ form.protocol.toUpperCase() }} 默认 {{ PROTOCOLS[form.protocol].port }}</span
            >
          </label>
        </template>
        <label v-if="form.protocol === 'nfs'" class="field span-2">
          <span class="field-label required">导出路径</span>
          <input
            v-model="form.base_path"
            name="export"
            class="mono"
            placeholder="/volume1/photos"
            autocapitalize="off"
            spellcheck="false"
            :aria-invalid="!!err('export')"
          />
          <span v-if="err('export')" class="field-error">{{ err('export') }}</span>
          <span v-else class="field-hint">在 NAS 的 NFS 设置里能看到，并要允许本机 IP 访问</span>
        </label>
        <template v-if="login">
          <label class="field">
            <span class="field-label">用户名</span>
            <input
              v-model="form.username"
              name="username"
              autocomplete="off"
              autocapitalize="off"
              spellcheck="false"
              :placeholder="form.protocol === 'ftp' ? '留空为匿名登录' : ''"
            />
          </label>
          <div v-if="form.protocol === 'sftp'" class="field">
            <span class="field-label">认证方式</span>
            <Segmented v-model="form.auth" :options="AUTH_OPTIONS" label="认证方式" />
          </div>
          <label v-if="!keyAuth" class="field" :class="{ 'span-2': isSftp }">
            <span class="field-label">密码</span>
            <input
              v-model="form.password"
              name="password"
              type="password"
              autocomplete="new-password"
              :placeholder="editing?.password_set ? '已保存，输入新密码可替换' : ''"
              :aria-invalid="!!err('password')"
            />
            <span v-if="err('password')" class="field-error">{{ err('password') }}</span>
          </label>
          <template v-else>
            <div class="field span-2">
              <span class="field-label" :class="{ required: !editing?.private_key_set }">私钥</span>
              <textarea
                v-model="form.private_key"
                name="private_key"
                class="mono key-input"
                rows="4"
                autocomplete="off"
                autocapitalize="off"
                spellcheck="false"
                :placeholder="editing?.private_key_set ? '已保存，粘贴新私钥可替换' : KEY_PLACEHOLDER"
                :aria-invalid="!!err('private_key')"
              />
              <span v-if="err('private_key')" class="field-error">{{ err('private_key') }}</span>
              <span v-else class="field-hint key-hint">
                粘贴私钥文件的全部内容，加密保存在本机。
                <label class="file-link">
                  从文件读取
                  <input type="file" class="visually-hidden" @change="readKeyFile" />
                </label>
              </span>
            </div>
            <label class="field span-2">
              <span class="field-label">私钥口令</span>
              <input
                v-model="form.key_passphrase"
                name="key_passphrase"
                type="password"
                autocomplete="new-password"
                :placeholder="editing?.key_passphrase_set ? '已保存，输入新口令可替换' : '私钥没有口令就留空'"
              />
            </label>
          </template>
        </template>
        <Alert v-if="!editing && meta.hint" tone="warning" class="span-2">{{ meta.hint }}</Alert>
      </div>

      <!-- connection result (wizard step 2 and editing) -->
      <Alert
        v-if="(showConn || step === 'dir') && probe.error && !probe.loading"
        tone="danger"
        :title="probe.hostKeyChanged ? '服务器指纹已变化' : `连接 ${where} 失败`"
      >
        {{ probe.error }}
        <p v-if="probe.detail" class="detail text-sm text-muted">{{ probe.detail }}</p>
        <p v-if="probe.hostKeyChanged" class="detail">
          如果你刚重装过服务器或换了机器，可以信任新指纹：<CopyText :value="probe.hostKey" />
          <button type="button" class="btn-sm" @click="trustNewKey">信任新指纹</button>
        </p>
      </Alert>

      <!-- 3. folder -->
      <template v-if="!editing && step === 'dir'">
        <p class="text-sm step-intro">
          <Icon name="check-circle" :size="14" class="text-success" />
          已连接 {{ where }}。选择要整理的{{ meta.pathLabel }}，进入后点「使用这个目录」。
        </p>
        <FolderPicker
          :protocol="form.protocol"
          :path="probe.path"
          :top="browseTop"
          :roots="roots"
          :dirs="probe.dirs"
          :loading="probe.loading"
          :top-label="topLabel"
          :empty-text="
            form.protocol === 'smb' && !probe.path
              ? '没有看到共享文件夹，检查账号是否有权限'
              : form.protocol === 'local' && !probe.path
                ? '没有允许的目录：启动时用 FE_LOCAL_ROOTS 指定'
                : undefined
          "
          @open="browse"
        />
        <p class="text-sm text-muted">
          将使用：<span class="mono">{{ probe.path || '—' }}</span>
          <template v-if="!probe.path && form.protocol === 'smb'">（先进入一个共享文件夹）</template>
        </p>
      </template>

      <!-- 4. name -->
      <div v-if="!editing && step === 'name'" class="form-grid" v-on="vn.form">
        <label class="field span-2">
          <span class="field-label required">名称</span>
          <input
            v-model="form.name"
            name="name"
            maxlength="64"
            :aria-invalid="!!vn.error('name')"
            @input="nameTouched = true"
          />
          <span v-if="vn.error('name')" class="field-error">{{ vn.error('name') }}</span>
          <span v-else class="field-hint">新建整理任务时用它来选存储</span>
        </label>
        <label class="field span-2">
          <span class="field-label">备注</span>
          <input v-model="form.description" name="description" placeholder="可不填" />
        </label>
        <dl class="kv span-2">
          <dt>连接方式</dt>
          <dd>{{ PROTOCOLS[form.protocol].title }}</dd>
          <template v-if="remote">
            <dt>地址</dt>
            <dd class="mono">{{ where }}</dd>
          </template>
          <dt>{{ meta.pathLabel }}</dt>
          <dd class="mono">{{ form.base_path }}</dd>
          <template v-if="form.protocol === 'sftp' && trustedKey">
            <dt>服务器指纹</dt>
            <dd><CopyText :value="trustedKey" /></dd>
          </template>
        </dl>
      </div>

      <!-- folder + note (editing) -->
      <div v-if="editing" class="form-grid" v-on="vn.form">
        <div v-if="form.protocol !== 'nfs'" class="field span-2">
          <span class="field-label required">{{ meta.pathLabel }}</span>
          <div class="path-row">
            <input
              v-model="form.base_path"
              name="base_path"
              class="mono"
              autocapitalize="off"
              spellcheck="false"
              :aria-invalid="!!vn.error('base_path')"
            />
            <button type="button" :disabled="probe.loading && browsing" @click="openBrowser">
              <Icon name="folder" :size="14" />浏览
            </button>
          </div>
          <span v-if="vn.error('base_path')" class="field-error">{{ vn.error('base_path') }}</span>
        </div>
        <div v-if="browsing && probe.ok" class="span-2 browse-block">
          <FolderPicker
            :protocol="form.protocol"
            :path="probe.path"
            :top="browseTop"
            :roots="roots"
            :dirs="probe.dirs"
            :loading="probe.loading"
            :top-label="topLabel"
            @open="browse"
          />
          <div class="browse-actions">
            <button type="button" class="ghost btn-sm" @click="browsing = false">收起</button>
            <button type="button" class="btn-sm" :disabled="!canUseFolder" @click="useFolder(probe.path)">
              使用这个目录
            </button>
          </div>
        </div>
        <Alert v-if="probe.ok && !browsing && !probe.loading" tone="success" class="span-2" title="连接正常">
          能读取 {{ where }} 的 {{ probe.path || form.base_path }}
        </Alert>
        <label class="field span-2">
          <span class="field-label">备注</span>
          <input v-model="form.description" name="description" />
        </label>
      </div>
    </form>

    <template #footer>
      <template v-if="editing">
        <button type="button" :disabled="probe.loading" @click="openBrowser">
          {{ probe.loading ? '测试中' : '测试连接' }}
        </button>
        <span class="foot-spacer" />
        <button type="button" @click="close">取消</button>
        <button type="submit" form="storage-form" class="primary" :disabled="saving">
          {{ saving ? '保存中' : '保存' }}
        </button>
      </template>
      <template v-else>
        <button v-if="!wizard.isFirst.value" type="button" class="ghost" @click="wizard.back">
          <Icon name="arrow-left" :size="14" />上一步
        </button>
        <span class="foot-spacer" />
        <button type="button" @click="close">取消</button>
        <button v-if="step === 'conn'" type="submit" form="storage-form" class="primary" :disabled="wizard.busy.value">
          {{ wizard.busy.value ? '正在连接' : '连接' }}
        </button>
        <button v-else-if="step === 'dir'" type="button" class="primary" :disabled="!canUseFolder" @click="wizard.next">
          使用这个目录
        </button>
        <button v-else-if="step === 'name'" type="submit" form="storage-form" class="primary" :disabled="saving">
          {{ saving ? '保存中' : '保存' }}
        </button>
      </template>
    </template>
  </BaseDialog>
</template>

<style scoped>
.choices {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: var(--space-2);
}
.choice {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--space-1);
  height: auto;
  padding: var(--space-3);
  text-align: left;
  white-space: normal;
  border-radius: var(--radius-md);
}
.choice.chosen {
  border-color: var(--accent);
}
.choice-title {
  font-weight: 600;
  color: var(--text);
}
.choice-desc {
  line-height: 1.5;
  font-size: var(--text-sm);
  color: var(--text-muted);
}
.step-intro {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  margin: 0;
}
.static-value {
  display: flex;
  align-items: center;
  min-height: 32px;
}
.key-input {
  resize: vertical;
  font-size: var(--text-sm);
}
.key-hint {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}
.file-link {
  color: var(--accent);
  cursor: pointer;
}
.visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
}
.detail {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin: var(--space-1) 0 0;
  word-break: break-all;
}
.path-row {
  display: flex;
  gap: var(--space-2);
}
.path-row input {
  flex: 1;
  min-width: 0;
}
.browse-block {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}
.browse-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
}
.foot-spacer {
  flex: 1;
}
</style>
