<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import ActionMenu from '@/base/ActionMenu.vue'
import BaseDialog from '@/base/BaseDialog.vue'
import Icon from '@/base/Icon.vue'
import Switch from '@/base/Switch.vue'
import TableState from '@/base/TableState.vue'
import { toast } from '@/base/toast'
import { useConfirm } from '@/base/useConfirm'
import { useDirtyGuard } from '@/base/useDirtyGuard'
import { useFetch } from '@/base/useFetch'
import { useFormValidation } from '@/base/useFormValidation'
import { createCategory, deleteCategory, listCategories, updateCategory } from '@/api/files'
import type { Category } from '@/api/types'

const props = defineProps<{ open: boolean; filesystemId: number }>()
const emit = defineEmits<{ close: []; changed: [] }>()
const confirm = useConfirm()

const list = useFetch((signal) => listCategories(props.filesystemId, signal))
watch(
  () => props.open,
  (o) => {
    if (o) void list.refresh()
  },
)

const empty = () => ({ name: '', path: '', description: '', agent_editable: true })
const form = reactive(empty())
const editing = ref<Category | null>(null)
const formOpen = ref(false)
const saving = ref(false)
const guard = useDirtyGuard(() => form)
const v = useFormValidation(() => ({
  name: form.name.trim() ? '' : '请输入名称',
  path: !form.path.trim() ? '请输入目录' : form.path.split('/').includes('..') ? '目录不能包含 ..' : '',
}))

function openForm(c: Category | null) {
  editing.value = c
  Object.assign(
    form,
    c ? { name: c.name, path: c.path, description: c.description, agent_editable: c.agent_editable } : empty(),
  )
  v.reset()
  guard.reset()
  formOpen.value = true
}

async function closeForm() {
  if (await guard.confirmDiscard()) formOpen.value = false
}

async function save() {
  if (!v.validate() || saving.value) return
  saving.value = true
  const body = {
    filesystem_id: props.filesystemId,
    name: form.name.trim(),
    path: `/${form.path.trim().replace(/^\/+|\/+$/g, '')}`,
    description: form.description.trim(),
    agent_editable: form.agent_editable,
  }
  try {
    if (editing.value) await updateCategory(editing.value.id, body)
    else await createCategory(body)
    toast.success(editing.value ? '已保存' : '已添加')
    guard.reset()
    formOpen.value = false
    void list.refresh()
    emit('changed')
  } catch (e) {
    toast.error(editing.value ? '保存失败' : '添加失败', e)
  } finally {
    saving.value = false
  }
}

async function remove(c: Category) {
  const ok = await confirm({
    message: `删除分类「${c.name}」？放在这个分类下的文件会退回重新整理。`,
    confirmText: '删除',
    danger: true,
  })
  if (!ok) return
  try {
    await deleteCategory(c.id)
    toast.success('已删除')
    void list.refresh()
    emit('changed')
  } catch (e) {
    toast.error('删除失败', e)
  }
}
</script>

<template>
  <BaseDialog :open="open" title="分类" width="720px" @close="emit('close')">
    <div class="toolbar">
      <span class="spacer" />
      <button type="button" class="primary btn-sm" @click="openForm(null)"><Icon name="plus" :size="14" />添加</button>
    </div>
    <div class="table-wrap">
      <table>
        <thead>
          <tr>
            <th>名称</th>
            <th>目录</th>
            <th class="col-actions">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="c in list.data.value ?? []" :key="c.id">
            <td>
              {{ c.name }}
              <span v-if="c.description" class="cell-sub">{{ c.description }}</span>
            </td>
            <td class="mono">{{ c.path }}</td>
            <td class="row-actions">
              <span>
                <button type="button" class="ghost btn-sm" @click="openForm(c)">编辑</button>
                <ActionMenu
                  :items="[{ label: '删除', icon: 'trash-2', danger: true, onSelect: () => void remove(c) }]"
                />
              </span>
            </td>
          </tr>
          <TableState
            :colspan="3"
            :loading="list.loading.value && !list.data.value"
            :error="list.error.value"
            :empty="!list.data.value?.length"
            empty-text="暂无分类"
            @retry="list.refresh"
          />
        </tbody>
      </table>
    </div>

    <BaseDialog :open="formOpen" :title="editing ? '编辑分类' : '添加分类'" width="480px" @close="closeForm">
      <form id="category-form" class="form" novalidate v-on="v.form" @submit.prevent="save">
        <label class="field">
          <span class="field-label required">名称</span>
          <input v-model="form.name" name="name" maxlength="64" placeholder="照片" :aria-invalid="!!v.error('name')" />
          <span v-if="v.error('name')" class="field-error">{{ v.error('name') }}</span>
        </label>
        <label class="field">
          <span class="field-label required">目录</span>
          <input v-model="form.path" name="path" placeholder="/照片" :aria-invalid="!!v.error('path')" />
          <span v-if="v.error('path')" class="field-error">{{ v.error('path') }}</span>
        </label>
        <label class="field">
          <span class="field-label">放什么</span>
          <textarea v-model="form.description" name="description" rows="3" placeholder="照片与图片，按年月归档" />
        </label>
        <div class="field">
          <span class="field-label">允许智能体修改</span>
          <Switch v-model="form.agent_editable" label="允许智能体修改" />
        </div>
      </form>
      <template #footer>
        <button type="button" @click="closeForm">取消</button>
        <button type="submit" form="category-form" class="primary" :disabled="saving">
          {{ saving ? '保存中' : '保存' }}
        </button>
      </template>
    </BaseDialog>
  </BaseDialog>
</template>
