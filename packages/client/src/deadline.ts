const maximumTimerDelay = 0x7fffffff

/** Schedule an asynchronous deadline without trusting host timer precision. */
export function scheduleDeadline(timeout: number, callback: () => void): () => void {
  // Preserve asynchronous expiry for non-positive and non-finite inputs.
  const budget = Number.isFinite(timeout) ? Math.max(0, timeout) : 0
  const started = performance.now()
  let action: (() => void) | undefined = callback
  let timer: ReturnType<typeof setTimeout> | undefined
  let checking = false
  const check = () => {
    if (!action || checking) return
    checking = true
    try {
      const remaining = budget - (performance.now() - started)
      const previous = timer
      timer = undefined
      if (remaining > 0) {
        if (previous !== undefined) clearTimeout(previous)
        if (action !== undefined) timer = setTimeout(check, Math.ceil(Math.min(maximumTimerDelay, remaining)))
      } else {
        const expire = action
        action = undefined
        if (previous !== undefined) clearTimeout(previous)
        expire()
      }
    } finally {
      checking = false
    }
  }
  timer = setTimeout(check, Math.ceil(Math.min(maximumTimerDelay, budget)))
  return () => {
    action = undefined
    const previous = timer
    timer = undefined
    if (previous !== undefined) clearTimeout(previous)
  }
}
