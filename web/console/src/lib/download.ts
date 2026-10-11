import type { Download } from '@/api'

/** Hands a fetched file to the browser as a download. */
export function saveDownload({ blob, filename }: Download) {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  a.rel = 'noopener'
  document.body.append(a)
  a.click()
  a.remove()
  // Revoked later so Safari has started the download first.
  setTimeout(() => URL.revokeObjectURL(url), 30_000)
}
