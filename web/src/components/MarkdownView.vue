<script setup lang="ts">
import { computed } from 'vue'
import MarkdownIt from 'markdown-it'
import DOMPurify from 'dompurify'

const props = defineProps<{ content: string }>()

const md = new MarkdownIt({
  html: false, // raw HTML in model output is never passed through
  linkify: true,
  breaks: true,
})

const rendered = computed(() => DOMPurify.sanitize(md.render(props.content || '')))
</script>

<template>
  <!-- eslint-disable-next-line vue/no-v-html -- sanitized by DOMPurify above -->
  <div class="md-body" v-html="rendered"></div>
</template>
