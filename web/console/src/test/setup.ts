import '@testing-library/jest-dom/vitest'
import { cleanup, configure } from '@testing-library/react'
import { toast } from 'sonner'
import { afterEach, vi } from 'vitest'

// The first test in a file also pays for transforming lazy route modules.
configure({ asyncUtilTimeout: 5000 })

afterEach(() => {
  cleanup()
  // sonner keeps toasts in a module-level store that outlives the Toaster.
  toast.dismiss()
  localStorage.clear()
  sessionStorage.clear()
})

// jsdom lacks the browser APIs Radix and the theme provider rely on.
if (!window.matchMedia) {
  window.matchMedia = (query: string) =>
    ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    }) as MediaQueryList
}

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= ResizeObserverStub as unknown as typeof ResizeObserver

Element.prototype.scrollIntoView ??= vi.fn()
Element.prototype.hasPointerCapture ??= () => false
Element.prototype.releasePointerCapture ??= () => {}
window.scrollTo = vi.fn() as unknown as typeof window.scrollTo
URL.createObjectURL ??= vi.fn(() => 'blob:test')
URL.revokeObjectURL ??= vi.fn()
