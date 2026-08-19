<template>
  <!-- 登录页（bare 布局，App.vue 不渲染 chrome）。凭据错误 401 由拦截器弹；
       成功按 redirect 回跳（仅接受站内路径，防 open-redirect）。 -->
  <AuthShell>
    <h1 class="auth-title">登录 Hify</h1>
    <p class="auth-sub">内部工具，使用账号密码登录</p>
    <el-form
      ref="formRef"
      :model="form"
      :rules="rules"
      label-position="top"
      @submit.prevent
      @keyup.enter="submit"
    >
      <el-form-item label="用户名" prop="username">
        <el-input
          v-model="form.username"
          placeholder="用户名"
          :prefix-icon="User"
          autocomplete="username"
        />
      </el-form-item>
      <el-form-item label="密码" prop="password">
        <el-input
          v-model="form.password"
          type="password"
          show-password
          placeholder="至少 8 位"
          :prefix-icon="Lock"
          autocomplete="current-password"
        />
      </el-form-item>
      <el-button
        class="auth-submit"
        type="primary"
        :loading="loading"
        @click="submit"
      >
        登录
      </el-button>
    </el-form>
    <p class="auth-switch">
      没有账号？<router-link class="auth-link" :to="{ name: 'register' }">去注册</router-link>
    </p>
  </AuthShell>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import type { FormInstance, FormRules } from 'element-plus'
import { Lock, User } from '@element-plus/icons-vue'
import AuthShell from '@/components/AuthShell.vue'
import { notifySuccess } from '@/utils/notify'
import { useAuthStore } from '@/stores/auth'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const formRef = ref<FormInstance>()
const loading = ref(false)
const form = ref({ username: '', password: '' })

// 对齐后端 binding：username 1-128、password 8-128
const rules: FormRules = {
  username: [{ required: true, message: '请输入用户名', trigger: 'blur' }],
  password: [
    { required: true, message: '请输入密码', trigger: 'blur' },
    { min: 8, max: 128, message: '密码长度 8-128 位', trigger: 'blur' },
  ],
}

async function submit(): Promise<void> {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return
  loading.value = true
  try {
    await auth.login({ ...form.value })
    notifySuccess('登录成功')
    const redirect = route.query.redirect
    await router.push(
      typeof redirect === 'string' && redirect.startsWith('/') ? redirect : '/',
    )
  } catch {
    // 凭据错误提示已由拦截器弹
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.auth-title {
  margin: 0 0 var(--hf-space-2);
  font-size: var(--hf-font-size-lg);
  font-weight: var(--hf-font-weight-semibold);
  color: var(--hf-text-1);
}

.auth-sub {
  margin: 0 0 var(--hf-space-6);
  font-size: var(--hf-font-size-base);
  color: var(--hf-text-3);
}

.auth-submit {
  width: 100%;
  margin-top: var(--hf-space-2);
}

.auth-switch {
  margin: var(--hf-space-6) 0 0;
  font-size: var(--hf-font-size-base);
  color: var(--hf-text-3);
  text-align: center;
}

.auth-link {
  color: var(--hf-primary-500);
  text-decoration: none;
}

.auth-link:hover {
  color: var(--hf-primary-600);
}
</style>
