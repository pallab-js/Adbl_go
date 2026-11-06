import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'

// Restore theme before first paint (UI-UX §3).
document.documentElement.dataset.theme =
  localStorage.getItem('adbl.theme') === 'light' ? 'light' : 'dark'

ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)
