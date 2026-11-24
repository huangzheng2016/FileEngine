<script setup lang="ts">
import { ref } from 'vue'
import Alert from '@/base/Alert.vue'
import Segmented from '@/base/Segmented.vue'
import StatusPill from '@/base/StatusPill.vue'
import { fmtLabel } from '@/base/format'
import { PROTOCOL } from '@/base/labels'
import type { Filesystem } from '@/api/types'
import type { StorageFormApi } from '@/lib/useStorageForm'
import { PROTOCOLS } from '@/lib/storage'

// Address, login and secrets of a storage. `editing` is the stored storage when one is being edited.
const props = defineProps<{ s: StorageFormApi; editing: Filesystem | null }>()
const { form, meta, remote, login, isNfs, isSftp, keyAuth, err, vc, readKeyFile } = props.s

const AUTH_OPTIONS = [
  { value: 'password' as const, label: '密码' },
  { value: 'key' as const, label: '私钥' },
]
const KEY_PLACEHOLDER = '-----BEGIN OPENSSH PRIVATE KEY-----\n…\n-----END OPENSSH PRIVATE KEY-----'

const keyFile = ref<HTMLInputElement | null>(null)
async function onKeyFile(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  await readKeyFile(file)
}
</script>

<template>
  <div class="form-grid" v-on="vc.form">
    <div v-if="editing" class="field">
      <span class="field-label">连接方式</span>
      <span class="static-value"><StatusPill :value="form.protocol" :map="PROTOCOL" /></span>
    </div>
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
      <label v-if="!isNfs" class="field">
        <span class="field-label">端口</span>
        <input v-model.number="form.port" name="port" type="number" min="1" max="65535" :aria-invalid="!!err('port')" />
        <span v-if="err('port')" class="field-error">{{ err('port') }}</span>
        <span v-else class="field-hint"
          >{{ fmtLabel(PROTOCOL, form.protocol) }} 默认 {{ PROTOCOLS[form.protocol].port }}</span
        >
      </label>
    </template>
    <label v-if="isNfs" class="field span-2">
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
      <span v-else class="field-hint">需在 NAS 上允许本机 IP 访问</span>
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
      <div v-if="isSftp" class="field">
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
          <span class="field-hint key-hint">
            粘贴私钥文件的全部内容
            <button type="button" class="ghost btn-sm" @click="keyFile?.click()">从文件读取</button>
            <input ref="keyFile" type="file" hidden @change="onKeyFile" />
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
</template>

<style scoped>
.static-value {
  display: flex;
  align-items: center;
  min-height: var(--control-h);
}
.key-input {
  resize: vertical;
  font-size: var(--text-sm);
}
.key-hint {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
}
</style>
