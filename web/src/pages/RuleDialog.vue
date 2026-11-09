<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import BaseDialog from '@/base/BaseDialog.vue'
import Segmented from '@/base/Segmented.vue'
import UnitInput from '@/base/UnitInput.vue'
import { labelOptions } from '@/base/format'
import { RULE_ROLE } from '@/base/labels'
import { toast } from '@/base/toast'
import { useDirtyGuard } from '@/base/useDirtyGuard'
import { useFormValidation } from '@/base/useFormValidation'
import { createRule, updateRule, type RuleInput } from '@/api/rules'
import type { PlanRule } from '@/api/types'
import { joinExts, regexError, splitExts, targetError } from '@/lib/rules'

const props = defineProps<{ open: boolean; sessionId: number; editing: PlanRule | null; position: number }>()
const emit = defineEmits<{ close: []; saved: [] }>()

const empty = () => ({
  name: '',
  kind: 'file' as 'file' | 'unit',
  exts: '',
  name_regex: '',
  path_prefix: '',
  min_size: 0,
  max_size: 0,
  target: '',
})
const form = reactive(empty())
const saving = ref(false)
const guard = useDirtyGuard(() => form)

const v = useFormValidation(() => ({
  name: form.name.trim() ? '' : '请输入名称',
  target: targetError(form.target),
  name_regex: regexError(form.name_regex),
  max_size: form.max_size && form.min_size > form.max_size ? '最大值不能小于最小值' : '',
}))

watch(
  () => props.open,
  (open) => {
    if (!open) return
    const r = props.editing
    Object.assign(
      form,
      r
        ? {
            name: r.name,
            kind: r.role === 'unit' ? 'unit' : 'file',
            exts: splitExts(r.exts).join(', '),
            name_regex: r.name_regex,
            path_prefix: r.path_prefix,
            min_size: r.min_size,
            max_size: r.max_size,
            target: r.target,
          }
        : empty(),
    )
    more.value = !!(form.path_prefix || form.name_regex || form.min_size || form.max_size)
    v.reset()
    guard.reset()
  },
)

const KINDS = labelOptions(RULE_ROLE) as { value: 'file' | 'unit'; label: string }[]
const more = ref(false)

const title = computed(() => (props.editing ? '编辑规则' : '新建规则'))

async function close() {
  if (await guard.confirmDiscard()) emit('close')
}

async function submit() {
  if (saving.value) return
  if (!v.validate()) {
    if (v.error('name_regex') || v.error('max_size')) more.value = true
    return
  }
  saving.value = true
  const body: RuleInput = {
    name: form.name.trim(),
    exts: joinExts(splitExts(form.exts)),
    name_regex: form.name_regex.trim(),
    path_prefix: form.path_prefix.trim().replace(/^\/+|\/+$/g, ''),
    file_type: form.kind === 'unit' ? 'directory' : 'file',
    role: form.kind,
    min_size: form.min_size,
    max_size: form.max_size,
    target: form.target.trim().replace(/^\/+/, ''),
  }
  try {
    if (props.editing) await updateRule(props.editing.id, body)
    else await createRule(props.sessionId, { ...body, position: props.position, source: 'user' })
    toast.success(props.editing ? '已保存' : '已创建')
    guard.reset()
    emit('saved')
    emit('close')
  } catch (e) {
    toast.error(props.editing ? '保存失败' : '创建失败', e)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <BaseDialog :open="open" :title="title" width="560px" @close="close">
    <form id="rule-form" class="form" novalidate v-on="v.form" @submit.prevent="submit">
      <label class="field">
        <span class="field-label required">名称</span>
        <input
          v-model="form.name"
          name="name"
          maxlength="64"
          placeholder="照片按年月"
          :aria-invalid="!!v.error('name')"
        />
        <span v-if="v.error('name')" class="field-error">{{ v.error('name') }}</span>
      </label>
      <div class="field">
        <span class="field-label">移动方式</span>
        <Segmented v-model="form.kind" :options="KINDS" label="移动方式" />
      </div>
      <label class="field">
        <span class="field-label">扩展名</span>
        <input v-model="form.exts" name="exts" placeholder="jpg, png" />
      </label>
      <details :open="more" @toggle="more = ($event.target as HTMLDetailsElement).open">
        <summary>更多条件</summary>
        <div class="form-grid more">
          <label class="field">
            <span class="field-label">位于目录</span>
            <input v-model="form.path_prefix" name="path_prefix" placeholder="下载" />
          </label>
          <label class="field">
            <span class="field-label">文件名正则</span>
            <input
              v-model="form.name_regex"
              name="name_regex"
              class="mono"
              placeholder="^IMG_\d+"
              :aria-invalid="!!v.error('name_regex')"
            />
            <span v-if="v.error('name_regex')" class="field-error">{{ v.error('name_regex') }}</span>
            <span v-else class="field-hint">留空不限</span>
          </label>
          <div class="field">
            <span class="field-label">最小</span>
            <UnitInput v-model="form.min_size" kind="bytes" name="min_size" label="最小" />
          </div>
          <div class="field">
            <span class="field-label">最大</span>
            <UnitInput
              v-model="form.max_size"
              kind="bytes"
              name="max_size"
              label="最大"
              :invalid="!!v.error('max_size')"
            />
            <span v-if="v.error('max_size')" class="field-error">{{ v.error('max_size') }}</span>
          </div>
        </div>
      </details>
      <label class="field">
        <span class="field-label required">目标位置</span>
        <input
          v-model="form.target"
          name="target"
          class="mono"
          placeholder="照片/{year}/{month}/{name}"
          :aria-invalid="!!v.error('target')"
        />
        <span v-if="v.error('target')" class="field-error">{{ v.error('target') }}</span>
        <span v-else class="field-hint">
          可用 {name} 文件名、{year} {month} 年月、{taken_year} {taken_month} 拍摄年月、{camera} 相机、{parent} 上级目录
        </span>
      </label>
    </form>
    <template #footer>
      <button type="button" @click="close">取消</button>
      <button type="submit" form="rule-form" class="primary" :disabled="saving">
        {{ saving ? '保存中' : '保存' }}
      </button>
    </template>
  </BaseDialog>
</template>

<style scoped>
.more {
  margin-top: var(--space-3);
}
</style>
