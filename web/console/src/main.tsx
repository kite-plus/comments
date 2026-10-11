import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { createBrowserRouter } from 'react-router'
import { App } from '@/app'
import { routes } from '@/routes'
import './index.css'

async function start() {
  if (__MOCK__) {
    const { installMocks } = await import('@/mocks')
    installMocks()
  }
  const router = createBrowserRouter(routes)
  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      <App router={router} />
    </StrictMode>
  )
}

void start()
