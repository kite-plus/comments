/** A comment's fixed link: the thread URL with #kc-<id> (widget.md section 8). */
export function permalink(threadUrl: string, commentId: string) {
  const base = threadUrl.split('#')[0]
  return `${base}#kc-${commentId}`
}
