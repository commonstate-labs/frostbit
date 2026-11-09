import { createApp } from 'vue'
import App from './App.vue'
import i18n from './i18n'
import { installTooltip } from './tooltip'
import './style.css'
import '../style.css'

installTooltip()
createApp(App).use(i18n).mount('#app')

