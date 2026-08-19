<template>
  <!-- 注册页（bare 布局）。注册成功即自动登录进系统（已确认的交互）；
       409 用户名已存在由拦截器弹、表单保持。 -->
  <AuthShell>
    <h1 class="auth-title">注册 Hify 账号</h1>
    <p class="auth-sub">内部工具开放注册，用户名唯一</p>
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
          autocomplete="new-password"
        />
      </el-form-item>
      <el-form-item label="确认密码" prop="confirm">
        <el-input
          v-model="form.confirm"
          type="password"
          show-password
          placeholder="再输入一次密码"
          :prefix-icon="Lock"
          autocomplete="new-password"
        />
      </el-form-item>
      <el-button
        class="auth-submit"
        type="primary"
        :loading="loading"
        @click="submit"
      >
        注册并登录
      </el-button>
    </el-form>
    <p class="auth-switch">
      已有账号？<router-link class="auth-link" :to="{ name: 'login' }">去登录</router-link>
    </p>
  </AuthShell>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import type { FormInstance, FormRules } from 'element-plus'
import { Lock, User } from '@element-plus/icons-vue'
import AuthShell from '@/components/AuthShell.vue'
import { register } from '@/api/auth'
import { notifySuccess } from '@/utils/notify'
import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const auth = useAuthStore()

const formRef = ref<FormInstance>()
const loading = ref(false)
const form = ref({ username: '', password: '', confirm: '' })

/** 确认密码一致性（前端专用字段，后端无此字段） */
function validateConfirm(
  _rule: unknown,
  value: string,
  callback: (error?: Error) => void,
): void {
  if (value !== form.value.password) {
    callback(new Error('两次输入的密码不一致'))
    return
  }
  callback()
}

// 对齐后端 binding：username 1-128、password 8-128
const rules: FormRules = {
  username: [{ required: true, message: '请输入用户名', trigger: 'blur' }],
  password: [
    { required: true, message: '请输入密码', trigger: 'blur' },
    { min: 8, max: 128, message: '密码长度 8-128 位', trigger: 'blur' },
  ],
  confirm: [
    { required: true, message: '请再次输入密码', trigger: 'blur' },
    { validator: validateConfirm, trigger: 'blur' },
  ],
}

async function submit(): Promise<void> {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return
  loading.value = true
  try {
    const { username, password } = form.value
    await register({ username, password })
    // 自动登录：注册成功即拿 session 进系统；
    // 极端情况（注册成功但登录网络失败）提示已由拦截器弹，可手动去登录页
    await auth.login({ username, password })
    notifySuccess('注册成功，已自动登录')
    await router.push('/')
  } catch {
    // 409 用户名已存在 / 校验失败提示已由拦截器弹；表单保持
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
