type Operation = () => unknown
let resultSink: unknown

export function measure(name: string, before: Operation, after: Operation): void {
  const format = (value: unknown) => JSON.stringify(value, (_, item: unknown) =>
    typeof item === 'bigint' ? item.toString() : item)
  if (format(before()) !== format(after())) throw new Error(`${name}: unequal output`)
  const batch = (operation: Operation) => {
    const start = performance.now()
    for (let index = 0; index < 2000; index++) resultSink = operation()
    return (performance.now() - start) * 1e6 / 2000
  }
  for (let warmup = 0; warmup < 12; warmup++) {
    if (warmup % 2) { batch(after); batch(before) } else { batch(before); batch(after) }
  }
  const oldSamples: number[] = [], newSamples: number[] = []
  for (let sample = 0; sample < 20; sample++) {
    if (sample % 2) { newSamples.push(batch(after)); oldSamples.push(batch(before)) }
    else { oldSamples.push(batch(before)); newSamples.push(batch(after)) }
  }
  report(name, oldSamples, newSamples)
}

export async function measureAsync(name: string, before: Operation, after: Operation): Promise<void> {
  const batch = async (operation: Operation) => {
    const start = performance.now()
    for (let index = 0; index < 2000; index++) resultSink = await operation()
    return (performance.now() - start) * 1e6 / 2000
  }
  for (let warmup = 0; warmup < 12; warmup++) {
    if (warmup % 2) { await batch(after); await batch(before) } else { await batch(before); await batch(after) }
  }
  const oldSamples: number[] = [], newSamples: number[] = []
  for (let sample = 0; sample < 20; sample++) {
    if (sample % 2) { newSamples.push(await batch(after)); oldSamples.push(await batch(before)) }
    else { oldSamples.push(await batch(before)); newSamples.push(await batch(after)) }
  }
  report(name, oldSamples, newSamples)
}

function report(name: string, before: number[], after: number[]): void {
  console.log(JSON.stringify({ name, before: statistics(before), after: statistics(after),
    beforeSamples: before, afterSamples: after, sinkPresent: resultSink !== undefined }))
}

function statistics(samples: number[]): { medianNs: number; minNs: number; maxNs: number } {
  const sorted = samples.toSorted((a, b) => a - b)
  return { medianNs: (sorted[9] + sorted[10]) / 2, minNs: sorted[0], maxNs: sorted[19] }
}
