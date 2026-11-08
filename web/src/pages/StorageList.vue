<script setup lang="ts">
import { ref } from 'vue'
import ActionMenu from '@/base/ActionMenu.vue'
import Icon from '@/base/Icon.vue'
import StatusPill from '@/base/StatusPill.vue'
import TableState from '@/base/TableState.vue'
import { PROTOCOL } from '@/base/labels'
import { toast } from '@/base/toast'
import { vTip } from '@/base/tooltip'
import { useConfirm } from '@/base/useConfirm'
import { useFetch } from '@/base/useFetch'
import { deleteFilesystem, listFilesystems } from '@/api/settings'
import type { Filesystem } from '@/api/types'
import { PROTOCOL_HINT } from '@/lib/storage'
import StorageFormDialog from './StorageFormDialog.vue'

const confirm = useConfirm()
const list = useFetch(listFilesystems, { immediate: true })

const open = ref(false)
const editing = ref<Filesystem | null>(null)
function edit(f: Filesystem | null) {
  editing.value = f
  open.value = true
}

const where = (f: Filesystem) =>
  f.protocol === 'local'
    ? f.base_path
    : `${f.host}${f.port ? `:${f.port}` : ''}${f.base_path ? ` / ${f.base_path}` : ''}`

async function remove(f: Filesystem) {
  if (
    !(await confirm({
      message: `删除存储「${f.name}」？存储里的文件不受影响。`,
      confirmText: '删除',
      danger: true,
    }))
  )
    return
  try {
    await deleteFilesystem(f.id)
    toast.success('已删除')
    void list.refresh()
  } catch (e) {
    toast.error('删除失败', e)
  }
}
</script>

<template>
  <div class="page">
    <div class="toolbar">
      <span class="spacer" />
      <button type="button" class="btn-icon" aria-label="刷新" :disabled="list.loading.value" @click="list.refresh">
        <Icon name="refresh-cw" />
      </button>
      <button type="button" class="primary" @click="edit(null)"><Icon name="plus" />新建</button>
    </div>
    <div class="table-wrap">
      <table>
        <thead>
          <tr>
            <th>名称</th>
            <th>连接方式</th>
            <th>位置</th>
            <th class="col-actions">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="f in list.data.value ?? []"
            :key="f.id"
            class="clickable"
            tabindex="0"
            @click="edit(f)"
            @keydown.enter.self.prevent="edit(f)"
          >
            <td>
              {{ f.name }}
              <span v-if="f.description" class="cell-sub">{{ f.description }}</span>
            </td>
            <td>
              <span v-tip="PROTOCOL_HINT[f.protocol]"><StatusPill :value="f.protocol" :map="PROTOCOL" /></span>
            </td>
            <td class="mono">
              <span class="truncate">{{ where(f) || '—' }}</span>
            </td>
            <td class="row-actions">
              <span>
                <button type="button" class="ghost btn-sm" @click.stop="edit(f)">编辑</button>
                <ActionMenu
                  :items="[{ label: '删除', icon: 'trash-2', danger: true, onSelect: () => void remove(f) }]"
                />
              </span>
            </td>
          </tr>
          <TableState
            :colspan="4"
            :loading="list.loading.value && !list.data.value"
            :error="list.error.value"
            :empty="!list.data.value?.length"
            empty-text="暂无存储"
            @retry="list.refresh"
          />
        </tbody>
      </table>
    </div>
    <StorageFormDialog :open="open" :editing="editing" @close="open = false" @saved="list.refresh" />
  </div>
</template>
