<script setup lang="ts">
import { RouterLink } from 'vue-router'
import EmptyState from '@/base/EmptyState.vue'
</script>

<template>
  <EmptyState icon="alert-triangle" title="地址有误或页面已移除">
    <RouterLink to="/sessions" class="btn">返回整理任务</RouterLink>
  </EmptyState>
</template>
