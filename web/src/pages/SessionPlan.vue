<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import ActionMenu from '@/base/ActionMenu.vue'
import BaseDialog from '@/base/BaseDialog.vue'
import DiffView from '@/base/DiffView.vue'
import EmptyState from '@/base/EmptyState.vue'
import Icon from '@/base/Icon.vue'
import Loading from '@/base/Loading.vue'
import MarkdownEditor from '@/base/MarkdownEditor.vue'
import StatusPill from '@/base/StatusPill.vue'
import TableState from '@/base/TableState.vue'
import { fmtNumber } from '@/base/format'
import { PLAN_STATUS } from '@/base/labels'
import { fmtRelative } from '@/base/time'
import { toast } from '@/base/toast'
import { vTip } from '@/base/tooltip'
import { useConfirm } from '@/base/useConfirm'
import { useDirtyGuard } from '@/base/useDirtyGuard'
import { useFetch } from '@/base/useFetch'
import { isConflict, toSec } from '@/api/http'
import { applyRulesSummary, deleteRule, listRules, previewRules, updateRule } from '@/api/rules'
import { approvePlan, draftPlan, getPlan, savePlan } from '@/api/runs'
import type { AxiosError } from 'axios'
import type { PlanRule, RulePreview, SessionPlan } from '@/api/types'
import StreamMarkdown from '@/components/StreamMarkdown.vue'
import { describeMatch } from '@/lib/rules'
import RuleDialog from './RuleDialog.vue'

const props = defineProps<{ sessionId: number; rev: number; busyReason: string }>()
const emit = defineEmits<{ started: [] }>()
const confirm = useConfirm()

const plan = useFetch((signal) => getPlan(props.sessionId, signal), { immediate: true })
const editing = ref(false)
const draftText = ref('')
const saving = ref(false)
const guard = useDirtyGuard(() => (editing.value ? draftText.value : ''), { route: true })

watch(
  () => props.rev,
  () => {
    if (!editing.value) void plan.refresh()
  },
)

const p = computed(() => plan.data.value)
const exists = computed(() => !!p.value && p.value.version > 0)

function edit() {
  draftText.value = p.value?.content ?? ''
  editing.value = true
  guard.reset()
}

async function cancelEdit() {
  if (draftText.value !== (p.value?.content ?? '') && !(await guard.confirmDiscard())) return
  editing.value = false
}

const conflict = ref<SessionPlan | null>(null)

async function save(base = p.value?.version ?? 0) {
  if (saving.value) return
  saving.value = true
  try {
    plan.data.value = await savePlan(props.sessionId, draftText.value, base)
    editing.value = false
    conflict.value = null
    toast.success('已保存')
  } catch (e) {
    if (isConflict(e)) {
      const latest = (e as AxiosError<{ plan?: SessionPlan }>).response?.data?.plan
      conflict.value = latest ?? (await getPlan(props.sessionId).catch(() => null))
    } else toast.error('保存失败', e)
  } finally {
    saving.value = false
  }
}

function discardMine() {
  if (!conflict.value) return
  plan.data.value = conflict.value
  draftText.value = conflict.value.content
  conflict.value = null
  toast.success('已载入最新方案')
}

// Stays set after a successful start until busyReason reports the run, so a second
// click cannot race the run event and hit 409.
const generating = ref(false)
watch(
  () => props.busyReason,
  (r) => {
    if (r) generating.value = false
  },
)
const generateBlocked = computed(() => props.busyReason || (generating.value ? '正在生成方案' : ''))

async function generate() {
  if (generateBlocked.value) return
  generating.value = true
  try {
    await draftPlan(props.sessionId, '')
    toast.success('已开始生成方案')
    emit('started')
  } catch (e) {
    generating.value = false
    toast.error('生成方案失败', e)
  }
}

const working = ref(false)
async function approve() {
  if (working.value) return
  working.value = true
  try {
    plan.data.value = await approvePlan(props.sessionId)
    toast.success('已批准')
  } catch (e) {
    toast.error('批准失败', e)
  } finally {
    working.value = false
  }
}

const planMenu = computed(() => [
  {
    label: '重新生成',
    icon: 'refresh-cw' as const,
    disabled: !!generateBlocked.value,
    title: generateBlocked.value || undefined,
    onSelect: () => void generate(),
  },
])

const rules = useFetch((signal) => listRules(props.sessionId, signal), { immediate: true })
const items = computed(() => rules.data.value ?? [])
watch(
  () => props.rev,
  () => void rules.refresh(),
)

const ruleOpen = ref(false)
const ruleEditing = ref<PlanRule | null>(null)
function openRule(r: PlanRule | null) {
  ruleEditing.value = r
  ruleOpen.value = true
}
const nextPosition = computed(() => items.value.reduce((m, r) => Math.max(m, r.position), 0) + 1)

const moving = ref(false)
async function move(r: PlanRule, dir: -1 | 1) {
  const i = items.value.indexOf(r)
  const other = items.value[i + dir]
  if (!other || moving.value) return
  moving.value = true
  try {
    await Promise.all([updateRule(r.id, { position: other.position }), updateRule(other.id, { position: r.position })])
    await rules.refresh()
  } catch (e) {
    toast.error('调整顺序失败', e)
  } finally {
    moving.value = false
  }
}

function moveBlocked(i: number, dir: -1 | 1): string {
  if (moving.value) return '正在调整顺序'
  if (dir < 0 && i === 0) return '已经是第一条'
  if (dir > 0 && i === items.value.length - 1) return '已经是最后一条'
  return ''
}

async function removeRule(r: PlanRule) {
  if (
    !(await confirm({
      message: `删除规则「${r.name}」？已按它决定的文件不受影响。`,
      confirmText: '删除',
      danger: true,
    }))
  )
    return
  try {
    await deleteRule(r.id)
    toast.success('已删除')
    void rules.refresh()
  } catch (e) {
    toast.error('删除失败', e)
  }
}

const ruleMenu = (r: PlanRule, i: number) => [
  {
    label: '上移',
    icon: 'arrow-up' as const,
    disabled: !!moveBlocked(i, -1),
    title: moveBlocked(i, -1) || undefined,
    onSelect: () => void move(r, -1),
  },
  {
    label: '下移',
    icon: 'arrow-down' as const,
    disabled: !!moveBlocked(i, 1),
    title: moveBlocked(i, 1) || undefined,
    onSelect: () => void move(r, 1),
  },
  { label: '删除', icon: 'trash-2' as const, danger: true, onSelect: () => void removeRule(r) },
]

const preview = ref<RulePreview[] | null>(null)
const previewing = ref(false)
const applying = ref(false)
const previewOf = (id: number) => preview.value?.find((x) => x.rule_id === id)

async function estimate() {
  if (previewing.value) return
  previewing.value = true
  try {
    preview.value = await previewRules(props.sessionId)
  } catch (e) {
    toast.error('预估失败', e)
  } finally {
    previewing.value = false
  }
}

async function apply() {
  if (applying.value || props.busyReason) return
  applying.value = true
  try {
    const r = await applyRulesSummary(props.sessionId)
    toast.success(
      r.skipped
        ? `已按规则决定 ${fmtNumber(r.applied)} 项，跳过 ${fmtNumber(r.skipped)} 项`
        : `已按规则决定 ${fmtNumber(r.applied)} 项`,
    )
    preview.value = null
    void rules.refresh()
  } catch (e) {
    toast.error('应用规则失败', e)
  } finally {
    applying.value = false
  }
}
</script>

<template>
  <div class="page">
    <section class="card">
      <div class="card-head">
        <div>
          <h3>方案</h3>
          <p v-if="exists && p" class="card-desc">
            版本 {{ p.version }} · 更新于 {{ fmtRelative(toSec(p.updated_at)) }}
          </p>
        </div>
        <StatusPill v-if="exists && p" :value="p.status" :map="PLAN_STATUS" />
        <template v-if="editing">
          <button type="button" class="btn-sm" @click="cancelEdit">取消</button>
          <button type="button" class="btn-sm" :disabled="saving" @click="save()">
            {{ saving ? '保存中' : '保存' }}
          </button>
        </template>
        <template v-else-if="exists">
          <button type="button" class="btn-sm" @click="edit"><Icon name="pencil" :size="14" />编辑</button>
          <button v-if="p?.status === 'draft'" type="button" class="btn-sm" :disabled="working" @click="approve">
            {{ working ? '批准中' : '批准' }}
          </button>
          <ActionMenu :items="planMenu" />
        </template>
      </div>
      <Loading v-if="!plan.loaded.value" />
      <p v-else-if="plan.error.value" class="text-danger">加载失败：{{ plan.error.value }}</p>
      <MarkdownEditor v-else-if="editing" v-model="draftText" :rows="20" name="plan" />
      <StreamMarkdown v-else-if="exists && p" :source="p.content" />
      <EmptyState v-else icon="list" title="还没有方案">
        <button v-tip="generateBlocked" type="button" :disabled="!!generateBlocked" @click="generate">
          {{ generating ? '生成中' : '生成方案' }}
        </button>
      </EmptyState>
    </section>

    <section class="rules">
      <div class="toolbar">
        <h3>规则</h3>
        <span class="spacer" />
        <button type="button" class="btn-sm" @click="openRule(null)"><Icon name="plus" :size="14" />新建</button>
        <button v-if="items.length" type="button" class="btn-sm" :disabled="previewing" @click="estimate">
          {{ previewing ? '预估中' : '预估命中' }}
        </button>
        <button
          v-if="items.length"
          v-tip="busyReason"
          type="button"
          class="btn-sm"
          :disabled="applying || !!busyReason"
          @click="apply"
        >
          {{ applying ? '应用中' : '应用规则' }}
        </button>
      </div>
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>名称</th>
              <th>匹配条件</th>
              <th>目标位置</th>
              <th class="num">命中</th>
              <th class="col-actions">操作</th>
            </tr>
          </thead>
          <tbody>
            <template v-for="(r, i) in items" :key="r.id">
              <tr class="clickable" tabindex="0" @click="openRule(r)" @keydown.enter.self.prevent="openRule(r)">
                <td>{{ r.name }}</td>
                <td class="wrap">{{ describeMatch(r) }}</td>
                <td class="mono">{{ r.target }}</td>
                <td class="num">{{ fmtNumber(previewOf(r.id)?.matched ?? r.matched) }}</td>
                <td class="row-actions">
                  <span>
                    <button type="button" class="ghost btn-sm" @click.stop="openRule(r)">编辑</button>
                    <ActionMenu :items="ruleMenu(r, i)" />
                  </span>
                </td>
              </tr>
              <tr v-if="previewOf(r.id)?.samples.length">
                <td colspan="5" class="wrap">
                  <span v-for="(x, n) in previewOf(r.id)!.samples" :key="n" class="cell-sub mono">
                    {{ x.original_path }} → {{ x.new_path }}
                  </span>
                </td>
              </tr>
            </template>
            <TableState
              :colspan="5"
              :loading="!rules.loaded.value"
              :error="rules.error.value"
              :empty="items.length === 0"
              empty-text="暂无规则"
              @retry="rules.refresh"
            />
          </tbody>
        </table>
      </div>
    </section>

    <BaseDialog :open="!!conflict" title="方案已被更新" width="720px" @close="conflict = null">
      <template v-if="conflict">
        <p>方案在你编辑时更新到了版本 {{ conflict.version }}。下面是它与你的修改的不同之处。</p>
        <DiffView
          :before="{ content: conflict.content }"
          :after="{ content: draftText }"
          :labels="{ content: '方案' }"
          before-label="最新版本"
          after-label="你的修改"
        />
      </template>
      <template #footer>
        <button type="button" @click="conflict = null">继续编辑</button>
        <button type="button" @click="discardMine">放弃我的修改</button>
        <button type="button" class="primary" :disabled="saving" @click="save(conflict!.version)">
          {{ saving ? '保存中' : '用我的修改覆盖' }}
        </button>
      </template>
    </BaseDialog>

    <RuleDialog
      :open="ruleOpen"
      :session-id="sessionId"
      :editing="ruleEditing"
      :position="nextPosition"
      @close="ruleOpen = false"
      @saved="(rules.refresh(), (preview = null))"
    />
  </div>
</template>

<style scoped>
.rules {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}
</style>
