<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import Alert from '@/base/Alert.vue'
import BaseDialog from '@/base/BaseDialog.vue'
import CopyText from '@/base/CopyText.vue'
import Select from '@/base/Select.vue'
import { errorMessage } from '@/base/errors'
import { labelOptions } from '@/base/format'
import { PROTOCOL } from '@/base/labels'
import { toast } from '@/base/toast'
import { useDirtyGuard } from '@/base/useDirtyGuard'
import { useFormValidation } from '@/base/useFormValidation'
import { createFilesystem, testFilesystem, updateFilesystem, type FilesystemInput } from '@/api/settings'
import type { Filesystem, Protocol } from '@/api/types'
import { PROTOCOL_HINT } from '@/lib/storage'

const props = defineProps<{ open: boolean; editing: Filesystem | null }>()
const emit = defineEmits<{ close: []; saved: [] }>()

const DEFAULT_PORT: Record<Protocol, number> = { local: 0, smb: 445, sftp: 22, nfs: 2049, ftp: 21 }

const empty = () => ({
  name: '',
  description: '',
  protocol: 'smb' as Protocol,
  base_path: '',
  host: '',
  port: DEFAULT_PORT.smb,
  username: '',
  password: '',
  key_path: '',
})
const form = reactive(empty())
const saving = ref(false)
const guard = useDirtyGuard(() => form)
const pathRequired = computed(() => form.protocol === 'local' || form.protocol === 'smb')
const remote = computed(() => form.protocol !== 'local')

const v = useFormValidation(() => ({
  name: form.name.trim() ? '' : '请输入名称',
  base_path:
    form.protocol === 'local' && !form.base_path.trim()
      ? '请输入本机目录'
      : form.protocol === 'smb' && !form.base_path.trim()
        ? '请输入共享名'
        : '',
  host: remote.value && !form.host.trim() ? '请输入地址' : '',
  port:
    remote.value && (!Number.isInteger(form.port) || form.port < 0 || form.port > 65535)
      ? '端口需在 0 到 65535 之间'
      : '',
}))

watch(
  () => props.open,
  (open) => {
    if (!open) return
    const f = props.editing
    Object.assign(form, f ? { ...empty(), ...f, password: '' } : empty())
    tested.value = null
    v.reset()
    guard.reset()
  },
)

watch(
  () => form.protocol,
  (p, old) => {
    if (!form.port || form.port === DEFAULT_PORT[old]) form.port = DEFAULT_PORT[p]
  },
)

const PROTOCOLS = labelOptions(PROTOCOL).map((o) => ({
  ...o,
  hint: o.value === 'smb' || o.value === 'sftp' ? '推荐' : undefined,
})) as {
  value: Protocol
  label: string
  hint?: string
}[]
const basePathLabel = computed(() =>
  form.protocol === 'smb'
    ? '共享名'
    : form.protocol === 'nfs'
      ? '导出路径'
      : form.protocol === 'local'
        ? '本机目录'
        : '根目录',
)

function body(): FilesystemInput {
  return {
    name: form.name.trim(),
    description: form.description.trim(),
    protocol: form.protocol,
    base_path: form.base_path.trim(),
    host: remote.value ? form.host.trim() : '',
    port: remote.value ? form.port : 0,
    username: remote.value ? form.username.trim() : '',
    password: remote.value && form.password ? form.password : undefined,
    key_path: form.protocol === 'sftp' ? form.key_path.trim() : '',
  }
}

interface TestResult {
  target: string
  ok: boolean
  text: string
  hostKey: string
}
const tested = ref<TestResult | null>(null)
const testing = ref(false)

async function test() {
  if (!v.validate() || testing.value) return
  const b = body()
  const target = remote.value ? `${b.host}${b.port ? `:${b.port}` : ''}` : b.base_path
  testing.value = true
  try {
    const r = await testFilesystem({ ...b, id: props.editing?.id })
    tested.value = { target, ok: r.success, text: r.error ?? '', hostKey: r.host_key ?? '' }
  } catch (e) {
    tested.value = { target, ok: false, text: errorMessage(e), hostKey: '' }
  } finally {
    testing.value = false
  }
}

async function close() {
  if (await guard.confirmDiscard()) emit('close')
}

async function submit() {
  if (!v.validate() || saving.value) return
  saving.value = true
  try {
    const b = body()
    const target = remote.value ? `${b.host}${b.port ? `:${b.port}` : ''}` : b.base_path
    if (b.protocol === 'sftp' && tested.value?.hostKey && tested.value.target === target) {
      b.host_key = tested.value.hostKey
    }
    if (props.editing) await updateFilesystem(props.editing.id, b)
    else await createFilesystem(b)
    toast.success(props.editing ? '已保存' : '已添加')
    guard.reset()
    emit('saved')
    emit('close')
  } catch (e) {
    toast.error(props.editing ? '保存失败' : '添加失败', e)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <BaseDialog :open="open" :title="editing ? '编辑存储' : '添加存储'" width="600px" @close="close">
    <form id="storage-form" class="form" novalidate v-on="v.form" @submit.prevent="submit">
      <div class="form-grid">
        <label class="field">
          <span class="field-label required">名称</span>
          <input
            v-model="form.name"
            name="name"
            maxlength="64"
            placeholder="家里的 NAS"
            :aria-invalid="!!v.error('name')"
          />
          <span v-if="v.error('name')" class="field-error">{{ v.error('name') }}</span>
        </label>
        <div class="field">
          <span class="field-label">连接方式</span>
          <Select v-model="form.protocol" :options="PROTOCOLS" label="连接方式" />
          <span class="field-hint">{{ PROTOCOL_HINT[form.protocol] }}</span>
        </div>
        <template v-if="remote">
          <label class="field">
            <span class="field-label required">地址</span>
            <input
              v-model="form.host"
              name="host"
              class="mono"
              placeholder="192.168.1.10"
              :aria-invalid="!!v.error('host')"
            />
            <span v-if="v.error('host')" class="field-error">{{ v.error('host') }}</span>
          </label>
          <label class="field">
            <span class="field-label">端口</span>
            <input
              v-model.number="form.port"
              name="port"
              type="number"
              min="0"
              max="65535"
              :aria-invalid="!!v.error('port')"
            />
            <span v-if="v.error('port')" class="field-error">{{ v.error('port') }}</span>
          </label>
        </template>
        <label class="field" :class="{ 'span-2': !remote }">
          <span class="field-label" :class="{ required: pathRequired }">
            {{ basePathLabel }}
          </span>
          <input
            v-model="form.base_path"
            name="base_path"
            class="mono"
            :placeholder="form.protocol === 'smb' ? 'share' : '/volume1/data'"
            :aria-invalid="!!v.error('base_path')"
          />
          <span v-if="v.error('base_path')" class="field-error">{{ v.error('base_path') }}</span>
        </label>
        <template v-if="remote && form.protocol !== 'nfs'">
          <label class="field">
            <span class="field-label">用户名</span>
            <input v-model="form.username" name="username" autocomplete="off" />
          </label>
          <label class="field">
            <span class="field-label">密码</span>
            <input
              v-model="form.password"
              name="password"
              type="password"
              autocomplete="new-password"
              :placeholder="editing?.password_set ? '已设置，输入新密码可替换' : ''"
            />
          </label>
        </template>
        <label v-if="form.protocol === 'sftp'" class="field span-2">
          <span class="field-label">私钥文件</span>
          <input v-model="form.key_path" name="key_path" class="mono" placeholder="/home/me/.ssh/id_ed25519" />
        </label>
        <label class="field span-2">
          <span class="field-label">备注</span>
          <input v-model="form.description" name="description" />
        </label>
      </div>
      <Alert v-if="tested" :tone="tested.ok ? 'success' : 'danger'" :title="tested.target">
        {{ tested.ok ? '连接成功' : `连接失败：${tested.text || '—'}` }}
        <p v-if="tested.hostKey" class="host-key">服务器指纹 <CopyText :value="tested.hostKey" /></p>
      </Alert>
    </form>
    <template #footer>
      <button type="button" :disabled="testing" @click="test">{{ testing ? '测试中' : '测试连接' }}</button>
      <button type="button" @click="close">取消</button>
      <button type="submit" form="storage-form" class="primary" :disabled="saving">
        {{ saving ? '保存中' : '保存' }}
      </button>
    </template>
  </BaseDialog>
</template>

<style scoped>
.host-key {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  flex-wrap: wrap;
}
</style>
