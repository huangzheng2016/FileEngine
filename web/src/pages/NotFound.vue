<script setup lang="ts">
import { RouterLink } from 'vue-router'
import EmptyState from '@/base/EmptyState.vue'
</script>

<template>
  <EmptyState icon="alert-triangle" title="页面不存在">
    <RouterLink to="/sessions" class="btn">返回整理任务</RouterLink>
  </EmptyState>
</template>
