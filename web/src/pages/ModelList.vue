<script setup lang="ts">
import { ref } from 'vue'
import ActionMenu from '@/base/ActionMenu.vue'
import Icon from '@/base/Icon.vue'
import StatusPill from '@/base/StatusPill.vue'
import TableState from '@/base/TableState.vue'
import { PROVIDER } from '@/base/labels'
import { toast } from '@/base/toast'
import { useConfirm } from '@/base/useConfirm'
import { useFetch } from '@/base/useFetch'
import { deleteModel, listModels } from '@/api/settings'
import type { ModelProvider } from '@/api/types'
import { effortLabel, fmtTokens } from '@/lib/modelForm'
import ModelFormDialog from './ModelFormDialog.vue'

const confirm = useConfirm()
const list = useFetch(listModels, { immediate: true })

const open = ref(false)
const editing = ref<ModelProvider | null>(null)
function edit(m: ModelProvider | null) {
  editing.value = m
  open.value = true
}

function host(m: ModelProvider) {
  try {
    return m.base_url ? new URL(m.base_url).host : '—'
  } catch {
    return m.base_url
  }
}

function abilities(m: ModelProvider) {
  return [
    m.vision_enabled ? '读图' : '',
    m.thinking_enabled ? `思考${m.reasoning_effort ? ` ${effortLabel(m.reasoning_effort)}` : ''}` : '',
  ]
    .filter(Boolean)
    .join(' · ')
}

async function remove(m: ModelProvider) {
  if (
    !(await confirm({
      message: `删除模型「${m.name}」？`,
      confirmText: '删除',
      danger: true,
    }))
  )
    return
  try {
    await deleteModel(m.id)
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
      <button type="button" class="primary" @click="edit(null)"><Icon name="plus" />新建模型</button>
    </div>
    <div class="table-wrap">
      <table>
        <thead>
          <tr>
            <th>名称</th>
            <th>接口</th>
            <th>地址</th>
            <th class="num">上下文</th>
            <th>能力</th>
            <th class="col-actions">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="m in list.data.value ?? []"
            :key="m.id"
            class="clickable"
            tabindex="0"
            @click="edit(m)"
            @keydown.enter.self.prevent="edit(m)"
          >
            <td>
              {{ m.name }}
              <span class="cell-sub mono">{{ m.model }}</span>
            </td>
            <td><StatusPill :value="m.provider" :map="PROVIDER" /></td>
            <td class="mono">{{ host(m) }}</td>
            <td class="num">{{ fmtTokens(m.context_window) }}</td>
            <td>{{ abilities(m) || '—' }}</td>
            <td class="row-actions">
              <ActionMenu :items="[{ label: '删除', icon: 'trash-2', danger: true, onSelect: () => void remove(m) }]" />
            </td>
          </tr>
          <TableState
            :colspan="6"
            :loading="list.loading.value && !list.data.value"
            :error="list.error.value"
            :empty="!list.data.value?.length"
            empty-text="暂无模型"
            @retry="list.refresh"
          >
            <button type="button" class="primary" @click="edit(null)">新建模型</button>
          </TableState>
        </tbody>
      </table>
    </div>
    <ModelFormDialog :open="open" :editing="editing" @close="open = false" @saved="list.refresh" />
  </div>
</template>
