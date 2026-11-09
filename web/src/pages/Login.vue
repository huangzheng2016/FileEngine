<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { login } from '@/api/http'
import { app } from '@/base/app'
import { errorMessage } from '@/base/errors'
import { useFormValidation } from '@/base/useFormValidation'
import { resetSignIn } from '@/router'

const route = useRoute()
const router = useRouter()

const form = reactive({ token: '' })
const submitting = ref(false)
const failure = ref('')

const v = useFormValidation(() => ({ token: form.token.trim() ? '' : '请输入访问令牌' }))

async function submit() {
  if (!v.validate() || submitting.value) return
  submitting.value = true
  failure.value = ''
  try {
    await login(form.token.trim())
    resetSignIn()
    const next = typeof route.query.next === 'string' && route.query.next.startsWith('/') ? route.query.next : '/'
    await router.replace(next)
  } catch (e) {
    failure.value = errorMessage(e)
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="login">
    <form class="card form login-card" novalidate v-on="v.form" @submit.prevent="submit">
      <h1>{{ app.name }}</h1>
      <label class="field">
        <span class="field-label">访问令牌</span>
        <input
          v-model="form.token"
          type="password"
          name="token"
          autocomplete="current-password"
          autofocus
          :aria-invalid="!!v.error('token')"
        />
        <span v-if="v.error('token')" class="field-error">{{ v.error('token') }}</span>
        <span v-else class="field-hint">见服务器配置文件中的 server.token</span>
      </label>
      <p v-if="failure" class="field-error" role="alert">{{ failure }}</p>
      <button type="submit" class="primary" :disabled="submitting">{{ submitting ? '登录中' : '登录' }}</button>
    </form>
  </div>
</template>

<style scoped>
.login {
  display: grid;
  place-items: center;
  min-height: 100dvh;
  padding: var(--space-4);
}
.login-card {
  width: min(360px, 100%);
  padding: var(--space-6);
}
</style>
