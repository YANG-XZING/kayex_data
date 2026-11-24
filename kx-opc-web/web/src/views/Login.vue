<script setup>
// 登录页（单独的整页，没有侧栏）。登录后回到原来要去的页面，没有则去该账户的首页。
import { ref, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import LOGO_URL from '../logo.js'
import { login, homeOf } from '../store.js'

const route = useRoute()
const router = useRouter()
const user = ref(localStorage.getItem('kx.lastUser') || '')
const password = ref('')
const error = ref('')
const busy = ref(false)
const userInput = ref(null)
const pwdInput = ref(null)

async function submit() {
  if (!user.value.trim() || !password.value) {
    error.value = '请输入用户名和密码'
    return
  }
  busy.value = true
  error.value = ''
  try {
    const me = await login(user.value.trim(), password.value)
    try { localStorage.setItem('kx.lastUser', user.value.trim()) } catch { /* 忽略 */ }
    const back = typeof route.query.redirect === 'string' ? route.query.redirect : ''
    router.replace(back || homeOf(me))
  } catch (e) {
    error.value = e.message
    password.value = ''
    pwdInput.value?.focus()
  } finally {
    busy.value = false
  }
}
onMounted(() => (user.value ? pwdInput : userInput).value?.focus())
</script>

<template>
  <div class="page">
    <form class="box card" @submit.prevent="submit">
      <div class="plate"><img :src="LOGO_URL" alt="LINTON" /></div>
      <h1>炉台数据平台</h1>
      <label class="field">
        <span>用户名</span>
        <input ref="userInput" v-model="user" class="input" autocomplete="username" />
      </label>
      <label class="field">
        <span>密码</span>
        <input ref="pwdInput" v-model="password" class="input" type="password" autocomplete="current-password" />
      </label>
      <div class="err">{{ error }}</div>
      <button class="btn primary go" type="submit" :disabled="busy">{{ busy ? '登录中…' : '登录' }}</button>
    </form>
  </div>
</template>

<style scoped>
.page { min-height: 100vh; display: grid; place-items: center; padding: 24px 16px;
  background:
    radial-gradient(600px 400px at 15% 10%, rgba(99, 102, 241, .35), transparent 70%),
    linear-gradient(160deg, #1b1d3f 0%, #171934 55%, #13152b 100%) }
.box { width: min(380px, 100%); padding: 30px 30px 26px; display: flex; flex-direction: column; gap: 14px }
.plate { height: 60px; border-radius: 13px; display: grid; place-items: center; padding: 8px 14px; background: #fff;
  box-shadow: inset 0 -2px 0 rgba(99, 102, 241, .18), 0 6px 20px rgba(15, 23, 42, .08) }
.plate img { max-width: 70%; max-height: 40px; object-fit: contain }
h1 { margin: 2px 0 6px; text-align: center; font-size: 20px; letter-spacing: 3px; font-weight: 800 }
.field { display: flex; flex-direction: column; gap: 6px; font-size: 13px; font-weight: 700; color: #334155 }
.field .input { padding: 10px 12px; font-size: 14px }
.err { min-height: 20px; color: var(--danger); font-size: 13px; font-weight: 600 }
.go { padding: 10px; font-size: 15px; justify-content: center }
.go:disabled { opacity: .6; cursor: wait }
</style>
