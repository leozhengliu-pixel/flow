/** Lightweight TTL map used by archived model loaders (Linear LoaderResultCache ≈ 2 min). */
const DEFAULT_TTL_MS = 2 * 60 * 1000;

type Entry<T> = { value: T; expiresAt: number };

export class LoaderResultCache<T> {
  private readonly store = new Map<string, Entry<T>>();
  private readonly ttlMs: number;

  constructor(ttlMs = DEFAULT_TTL_MS) {
    this.ttlMs = ttlMs;
  }

  get(key: string): T | undefined {
    const entry = this.store.get(key);
    if (!entry) return undefined;
    if (Date.now() > entry.expiresAt) {
      this.store.delete(key);
      return undefined;
    }
    return entry.value;
  }

  has(key: string) {
    return this.get(key) !== undefined;
  }

  set(key: string, value: T) {
    this.store.set(key, { value, expiresAt: Date.now() + this.ttlMs });
  }

  expire(key?: string) {
    if (key) this.store.delete(key);
    else this.store.clear();
  }
}
