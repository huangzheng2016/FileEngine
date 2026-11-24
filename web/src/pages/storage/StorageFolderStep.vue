<script setup lang="ts">
import Alert from '@/base/Alert.vue'
import { vTip } from '@/base/tooltip'
import PathInput from '@/components/PathInput.vue'
import type { StorageFormApi } from '@/lib/useStorageForm'
import FolderPicker from '../FolderPicker.vue'

// The folder to organize: a step of the new-storage flow, or a path field with a browser when editing.
const props = defineProps<{ s: StorageFormApi; editing: boolean }>()
const { form, meta, probe, roots, browseTop, topLabel, where, browser, vn, folderBlocked, canUseFolder } = props.s
</script>

<template>
  <template v-if="!editing">
    <p class="text-sm">已连接 {{ where }}，请选择{{ meta.pathLabel }}</p>
    <FolderPicker
      :protocol="form.protocol"
      :path="probe.path"
      :top="browseTop"
      :roots="roots"
      :dirs="probe.dirs"
      :loading="probe.loading"
      :top-label="topLabel"
      @open="browser.open"
    >
      <template #empty>
        <template v-if="!probe.path && form.protocol === 'smb'">没有看到共享文件夹，检查账号是否有权限</template>
        <template v-else-if="!probe.path && form.protocol === 'local'">
          没有允许的文件夹，请在启动时用 <code>FE_LOCAL_ROOTS</code> 指定
        </template>
        <template v-else>这里没有子文件夹</template>
      </template>
    </FolderPicker>
    <p class="text-sm text-muted">
      将使用：<span class="mono">{{ probe.path || '—' }}</span>
    </p>
  </template>
  <div v-else class="form-grid" v-on="vn.form">
    <div v-if="!s.isNfs.value" class="field span-2">
      <span class="field-label required">{{ meta.pathLabel }}</span>
      <PathInput
        v-model="form.base_path"
        name="base_path"
        :invalid="!!vn.error('base_path')"
        :browse-blocked="probe.loading && s.browsing.value ? '正在读取' : ''"
        @browse="s.openBrowser"
      />
      <span v-if="vn.error('base_path')" class="field-error">{{ vn.error('base_path') }}</span>
    </div>
    <div v-if="s.browsing.value && probe.ok" class="span-2 browse-block">
      <FolderPicker
        :protocol="form.protocol"
        :path="probe.path"
        :top="browseTop"
        :roots="roots"
        :dirs="probe.dirs"
        :loading="probe.loading"
        :top-label="topLabel"
        @open="browser.open"
      />
      <div class="browse-actions">
        <button type="button" class="ghost btn-sm" @click="s.closeBrowser()">收起</button>
        <button
          v-tip="folderBlocked"
          type="button"
          class="btn-sm"
          :disabled="!canUseFolder"
          @click="s.useFolder(probe.path)"
        >
          使用这个文件夹
        </button>
      </div>
    </div>
    <Alert v-if="probe.ok && !s.browsing.value && !probe.loading" tone="success" class="span-2" title="连接正常">
      能读取 {{ where }} 的 {{ probe.path || form.base_path }}
    </Alert>
  </div>
</template>

<style scoped>
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
</style>
