import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
// Стили уведомлений — обычным файлом, а не как sonner вставляет их сам: тегом
// <style> во время работы. Панель отдаёт Content-Security-Policy:
// default-src 'self', встроенные стили ей запрещены, и браузер молча
// отбрасывал этот тег. Уведомления рисовались без разметки и уезжали под
// нижний край экрана — около четырёх недель ни одно не было видно.
// Файл уходит в собранный CSS, отдаётся с того же адреса и политикой
// разрешён; ослаблять её ради этого не нужно (amnezia-vpn-server-omsa).
import 'sonner/dist/styles.css'
import App from './App.tsx'
import { initTheme } from './lib/theme.ts'

initTheme()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
