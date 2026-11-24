<script setup lang="ts">
// Version history of the agent settings: every save and what it changed.
import { watch } from 'vue'
import Pagination from '@/base/Pagination.vue'
import TableState from '@/base/TableState.vue'
import { AGENT_SETTING_FIELDS } from '@/base/labels'
import { fmtRelative, fmtTime } from '@/base/time'
import { vTip } from '@/base/tooltip'
import { useList } from '@/base/useList'
import { listConfigVersions } from '@/api/settings'

const props = defineProps<{ currentVersion: number }>()

const versions = useList((p, signal) => listConfigVersions(p.page, p.size, signal), {})

// a save on the form tab shows up here without a manual refresh
watch(
  () => props.currentVersion,
  () => void versions.refresh(),
)

const changedText = (keys: string[]) =>
  keys.length ? keys.map((k) => AGENT_SETTING_FIELDS[k] ?? '其他设置').join('、') : '—'
</script>

<template>
  <section class="card">
    <div class="table-wrap">
      <table>
        <thead>
          <tr>
            <th>版本</th>
            <th>改动</th>
            <th class="num">保存时间</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in versions.items.value" :key="row.version">
            <td class="tabular">
              版本 {{ row.version }}
              <span v-if="row.version === currentVersion" class="pill tone-accent">当前</span>
            </td>
            <td class="wrap">{{ changedText(row.changed) }}</td>
            <td v-tip="fmtTime(row.saved_at)" class="num">{{ fmtRelative(row.saved_at) }}</td>
          </tr>
          <TableState
            :colspan="3"
            :loading="versions.loading.value"
            :error="versions.error.value"
            :empty="versions.items.value.length === 0"
            empty-text="尚未保存过设置"
            @retry="versions.refresh"
          />
        </tbody>
      </table>
    </div>
    <Pagination
      :total="versions.total.value"
      :page="versions.page.value"
      :size="versions.size.value"
      @update:page="versions.setPage"
      @update:size="versions.setSize"
    />
  </section>
</template>
