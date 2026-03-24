import { createApp } from 'vue'
import ElementPlus from 'element-plus'
import zhLocale from 'element-plus/es/locale/lang/zh-cn'
import enLocale from 'element-plus/es/locale/lang/en'
import 'element-plus/dist/index.css'
import * as ElementPlusIconsVue from '@element-plus/icons-vue'
import router from './router'
import i18n, { getCurrentLocale } from './i18n'
import App from './App.vue'

const app = createApp(App)

// Element Plus locale follows i18n setting
const elLocale = getCurrentLocale() === 'zh-CN' ? zhLocale : enLocale
app.use(ElementPlus, { locale: elLocale })
app.use(router)
app.use(i18n)

for (const [key, component] of Object.entries(ElementPlusIconsVue)) {
  app.component(key, component)
}

app.mount('#app')
