/** Full-page navigation, behind an object so tests can observe it. */
export const browser = {
  assign(url: string) {
    window.location.assign(url)
  },
}
