<script setup lang="ts">
import CopyText from '@/base/CopyText.vue'
import StatusPill from '@/base/StatusPill.vue'
import { PROTOCOL } from '@/base/labels'
import type { StorageFormApi } from '@/lib/useStorageForm'

// Name and note. A new storage also gets a summary of what is about to be saved.
const props = defineProps<{ s: StorageFormApi; editing: boolean }>()
const { form, meta, remote, where, vn } = props.s
</script>

<template>
  <div class="form-grid" v-on="vn.form">
    <label class="field" :class="{ 'span-2': !editing }">
      <span class="field-label required">名称</span>
      <input v-model="form.name" name="name" maxlength="64" :aria-invalid="!!vn.error('name')" @input="s.touchName()" />
      <span v-if="vn.error('name')" class="field-error">{{ vn.error('name') }}</span>
    </label>
    <label class="field" :class="{ 'span-2': !editing }">
      <span class="field-label">备注</span>
      <input v-model="form.description" name="description" placeholder="可不填" />
    </label>
    <dl v-if="!editing" class="kv span-2">
      <dt>连接方式</dt>
      <dd><StatusPill :value="form.protocol" :map="PROTOCOL" /></dd>
      <template v-if="remote">
        <dt>地址</dt>
        <dd class="mono">{{ where }}</dd>
      </template>
      <dt>{{ meta.pathLabel }}</dt>
      <dd class="mono">{{ form.base_path }}</dd>
      <template v-if="form.protocol === 'sftp' && s.trustedKey.value">
        <dt>服务器指纹</dt>
        <dd><CopyText :value="s.trustedKey.value" /></dd>
      </template>
    </dl>
  </div>
</template>
