<template>
  <section class="page-card">
    <header class="page-header">
      <div class="page-titles">
        <span class="page-eyebrow">FileEngine</span>
        <h2 class="page-heading">{{ $t('dashboard.title') }}</h2>
        <p class="page-sub">{{ $t('dashboard.subtitle') }}</p>
      </div>
      <div class="page-actions">
        <el-button @click="load">
          <el-icon style="margin-right: 4px"><Refresh /></el-icon>{{ $t('common.refresh') }}
        </el-button>
      </div>
    </header>

    <div class="page-body" v-loading="loading">
      <!-- Journey: source -> scan -> tag -> execute -->
      <div class="journey">
        <div v-for="(step, i) in steps" :key="step.key" class="journey__item">
          <div class="step-card" :class="`is-${step.state}`">
            <div class="step-card__head">
              <span class="step-card__num">{{ i + 1 }}</span>
              <span class="step-card__title">{{ step.title }}</span>
              <span class="status-chip" :class="stateChipClass(step.state)">
                <span v-if="step.state === 'active'" class="status-dot"></span>
                {{ stateLabel(step.state) }}
              </span>
            </div>
            <div class="step-card__status">{{ step.status }}</div>
            <div class="step-card__cta">
              <router-link :to="step.cta.to">
                <el-button :type="step.state === 'done' ? 'default' : 'primary'" size="small">
                  {{ step.cta.label }}
                </el-button>
              </router-link>
            </div>
          </div>
          <el-icon v-if="i < steps.length - 1" class="journey__arrow"><ArrowRight /></el-icon>
        </div>
      </div>

      <!-- Current session snapshot -->
      <div v-if="latestSession" class="session-strip">
        <span class="session-strip__label">{{ $t('dashboard.latestSession') }}</span>
        <code class="cell-mono">#{{ latestSession.id }} {{ latestSession.root_path === '.' ? $t('tasks.allDirectories') : latestSession.root_path }}</code>
        <span class="status-chip" :class="sessionChipClass(latestSession.status)">
          <span v-if="isActive(latestSession)" class="status-dot"></span>
          {{ sessionStatusLabel(latestSession.status) }}
        </span>
        <span class="metric-chip"><span class="metric-label">{{ $t('tasks.totalFiles') }}</span><span class="metric-value">{{ latestSession.total_files }}</span></span>
        <span class="metric-chip"><span class="metric-label">{{ $t('tasks.taggedFiles') }}</span><span class="metric-value">{{ latestSession.tagged_files }}</span></span>
        <span class="metric-chip"><span class="metric-label">{{ $t('tasks.plannedOps') }}</span><span class="metric-value">{{ latestSession.planned_ops }}</span></span>
        <span class="metric-chip"><span class="metric-label">{{ $t('tasks.executedOps') }}</span><span class="metric-value">{{ latestSession.executed_ops }}</span></span>
        <router-link :to="{ name: 'tasks', query: { session: String(latestSession.id) } }" class="cell-link" style="margin-left: auto">
          {{ $t('dashboard.ctaViewSession') }}
        </router-link>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { listFilesystems, listSessions } from '../api'
import type { Filesystem, ScanSession } from '../types'
import { Refresh, ArrowRight } from '@element-plus/icons-vue'

const { t } = useI18n()
const filesystems = ref<Filesystem[]>([])
const sessions = ref<ScanSession[]>([])
const loading = ref(false)

onMounted(() => load())

async function load() {
  loading.value = true
  try {
    const [fsRes, sessRes] = await Promise.all([listFilesystems(), listSessions()])
    filesystems.value = fsRes.data
    sessions.value = sessRes.data
  } finally {
    loading.value = false
  }
}

const latestSession = computed(() => sessions.value[0]) // API orders id DESC
const anyScanning = computed(() => sessions.value.some(s => s.status === 'scanning'))

type StepState = 'todo' | 'active' | 'done'
interface Step {
  key: string
  title: string
  status: string
  state: StepState
  cta: { label: string; to: any }
}

const steps = computed<Step[]>(() => {
  const s = latestSession.value
  // 1. Source
  const fsCount = filesystems.value.length
  const sourceStep: Step = {
    key: 'source',
    title: t('dashboard.stepSource'),
    status: fsCount > 0 ? t('dashboard.sourceStatus', { count: fsCount }) : t('dashboard.sourceEmpty'),
    state: fsCount > 0 ? 'done' : 'todo',
    cta: fsCount > 0
      ? { label: t('dashboard.ctaManageFs'), to: '/settings/filesystems' }
      : { label: t('dashboard.ctaAddFs'), to: '/settings/filesystems' },
  }
  // 2. Scan
  const scanned = sessions.value.some(x => x.total_files > 0)
  const scanStep: Step = {
    key: 'scan',
    title: t('dashboard.stepScan'),
    status: anyScanning.value
      ? t('dashboard.scanActive')
      : (sessions.value.length > 0 ? t('dashboard.scanStatus', { count: sessions.value.length }) : t('dashboard.scanEmpty')),
    state: anyScanning.value ? 'active' : (scanned ? 'done' : 'todo'),
    cta: { label: t('dashboard.ctaNewScan'), to: { name: 'tasks', query: { new: '1' } } },
  }
  // 3. Tag
  let tagStep: Step
  if (!s || s.total_files === 0) {
    tagStep = { key: 'tag', title: t('dashboard.stepTag'), status: t('dashboard.tagEmpty'), state: 'todo', cta: { label: t('dashboard.ctaTag'), to: latestSessionTo(s) } }
  } else {
    const done = s.tagged_files >= s.total_files
    tagStep = {
      key: 'tag',
      title: t('dashboard.stepTag'),
      status: t('dashboard.tagProgress', { tagged: s.tagged_files, total: s.total_files }),
      state: done ? 'done' : (s.tagged_files > 0 || s.status === 'tagging' ? 'active' : 'todo'),
      cta: done
        ? { label: t('dashboard.ctaReview'), to: filesLink(s) }
        : { label: t('dashboard.ctaTag'), to: latestSessionTo(s) },
    }
  }
  // 4. Execute
  let executeStep: Step
  if (!s || s.planned_ops === 0) {
    executeStep = { key: 'execute', title: t('dashboard.stepExecute'), status: t('dashboard.executeEmpty'), state: 'todo', cta: { label: t('dashboard.ctaExecute'), to: latestSessionTo(s) } }
  } else {
    const done = s.executed_ops >= s.planned_ops
    executeStep = {
      key: 'execute',
      title: t('dashboard.stepExecute'),
      status: t('dashboard.executeStatus', { planned: s.planned_ops, executed: s.executed_ops }),
      state: done ? 'done' : 'active',
      cta: done
        ? { label: t('dashboard.ctaFiles'), to: filesLink(s) }
        : { label: t('dashboard.ctaExecute'), to: latestSessionTo(s) },
    }
  }
  return [sourceStep, scanStep, tagStep, executeStep]
})

function latestSessionTo(s?: ScanSession) {
  return s ? { name: 'tasks', query: { session: String(s.id) } } : { name: 'tasks' }
}

function filesLink(s: ScanSession) {
  return { name: 'files', query: { fs: String(s.filesystem_id), session: String(s.id) } }
}

function isActive(s: ScanSession): boolean {
  return s.status === 'scanning' || s.status === 'tagging' || s.status === 'executing'
}

function stateLabel(state: StepState): string {
  const map: Record<StepState, string> = { todo: t('dashboard.stateTodo'), active: t('dashboard.stateActive'), done: t('dashboard.stateDone') }
  return map[state]
}

function stateChipClass(state: StepState): string {
  const map: Record<StepState, string> = { todo: 'is-muted', active: 'is-warning', done: 'is-live' }
  return map[state]
}

function sessionStatusLabel(status: string): string {
  if (status.startsWith('error')) return t('tasks.statusError')
  return t('tasks.status_' + status) || status
}

function sessionChipClass(status: string): string {
  if (status === 'done' || status === 'tagged') return 'is-live'
  if (status.startsWith('error')) return 'is-danger'
  if (status === 'scanning' || status === 'tagging' || status === 'executing') return 'is-warning'
  return 'is-muted'
}
</script>

<style scoped>
.journey {
  display: flex;
  align-items: stretch;
  gap: 10px;
}

.journey__item {
  display: flex;
  align-items: stretch;
  gap: 10px;
  flex: 1;
  min-width: 0;
}

.journey__item:last-child {
  flex: 1;
}

.journey__arrow {
  align-self: center;
  color: var(--text-muted);
  flex-shrink: 0;
}

.step-card {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 18px;
  border-radius: var(--radius-md);
  background: var(--surface-elevated);
  border: 1px solid var(--border-soft);
  box-shadow: var(--inner-highlight);
  backdrop-filter: blur(12px);
  transition: all var(--duration-fast) var(--ease-spring);
}

.step-card:hover {
  transform: translateY(-1px);
  border-color: var(--floating-border);
  box-shadow: var(--shadow-soft), var(--inner-highlight);
}

.step-card.is-active {
  border-color: var(--floating-border);
  background: var(--accent-soft);
}

.step-card__head {
  display: flex;
  align-items: center;
  gap: 10px;
}

.step-card__num {
  width: 28px;
  height: 28px;
  display: grid;
  place-items: center;
  border-radius: 50%;
  font-family: var(--font-mono);
  font-size: 13px;
  font-weight: 700;
  color: var(--accent);
  background: var(--accent-soft);
  border: 1px solid var(--floating-border);
  flex-shrink: 0;
}

.step-card.is-done .step-card__num {
  color: var(--success);
  background: var(--success-soft);
  border-color: color-mix(in srgb, var(--success) 22%, transparent);
}

.step-card__title {
  font-size: 15px;
  font-weight: 700;
  color: var(--text-primary);
  letter-spacing: -0.01em;
  flex: 1;
  min-width: 0;
}

.step-card__status {
  font-size: 13px;
  color: var(--text-secondary);
  min-height: 40px;
}

.step-card__cta {
  margin-top: auto;
}

.session-strip {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  padding: 12px 16px;
  border-radius: var(--radius-md);
  background: var(--surface-elevated);
  border: 1px solid var(--border-soft);
}

.session-strip__label {
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.02em;
  color: var(--text-muted);
}

@media (max-width: 1100px) {
  .journey {
    flex-direction: column;
  }
  .journey__arrow {
    transform: rotate(90deg);
  }
}
</style>
