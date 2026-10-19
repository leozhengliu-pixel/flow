/** A FIFO with an async take() that can be cancelled (stop). */
export class AsyncQueue<T> {
  private readonly items: T[] = []
  private waiters: Array<(item: T | undefined) => void> = []
  private closed = false

  push(item: T): void {
    if (this.closed) return
    const waiter = this.waiters.shift()
    if (waiter) waiter(item)
    else this.items.push(item)
  }

  get size(): number {
    return this.items.length
  }

  /** Next item, or undefined once the queue is closed. */
  take(): Promise<T | undefined> {
    if (this.items.length) return Promise.resolve(this.items.shift())
    if (this.closed) return Promise.resolve(undefined)
    return new Promise((resolve) => this.waiters.push(resolve))
  }

  close(): void {
    this.closed = true
    const waiters = this.waiters
    this.waiters = []
    for (const waiter of waiters) waiter(undefined)
  }
}
