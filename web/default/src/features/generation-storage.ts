/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/

/**
 * Client-only persistence for the image/video generation workspaces.
 *
 * Task metadata and signed result URLs live in localStorage. Image generation
 * additionally stores base64 results as browser-local IndexedDB Blobs. None of
 * this adds application-server storage, CPU, or background work. Records are
 * scoped to the signed-in user and expire automatically after 24 hours.
 */

export const GENERATION_RESULT_TTL_MS = 24 * 60 * 60 * 1000
export const GENERATION_PENDING_TTL_MS = 20 * 60 * 1000
export const GENERATION_HISTORY_LIMIT = 12

type StoredGeneration<T> = {
  savedAt: number
  value: T
}

const STORAGE_PREFIX = 'newapi:generation:v1'

export function getGenerationUserScope(): string {
  if (typeof window === 'undefined') return 'server'

  try {
    const uid = window.localStorage.getItem('uid')?.trim()
    if (uid) return uid

    const rawUser = window.localStorage.getItem('user')
    if (rawUser) {
      const user = JSON.parse(rawUser) as { id?: number | string }
      if (user.id !== undefined && user.id !== null) return String(user.id)
    }
  } catch {
    // Storage may be unavailable or contain malformed legacy data.
  }

  return 'anonymous'
}

function storageKey(name: string): string {
  return `${STORAGE_PREFIX}:${getGenerationUserScope()}:${name}`
}

export function readGenerationRecord<T>(
  name: string,
  ttlMs = GENERATION_RESULT_TTL_MS
): StoredGeneration<T> | null {
  if (typeof window === 'undefined') return null

  try {
    const raw = window.localStorage.getItem(storageKey(name))
    if (!raw) return null

    const record = JSON.parse(raw) as StoredGeneration<T>
    if (
      !record ||
      typeof record.savedAt !== 'number' ||
      Date.now() - record.savedAt > ttlMs
    ) {
      window.localStorage.removeItem(storageKey(name))
      return null
    }

    return record
  } catch {
    return null
  }
}

export function writeGenerationRecord<T>(name: string, value: T): void {
  if (typeof window === 'undefined') return

  try {
    const record: StoredGeneration<T> = {
      savedAt: Date.now(),
      value,
    }
    window.localStorage.setItem(storageKey(name), JSON.stringify(record))
  } catch {
    // Quota/private-mode errors must never break generation itself.
  }
}

export function readGenerationHistory<T>(
  name: string,
  ttlMs = GENERATION_RESULT_TTL_MS
): T[] {
  const record = readGenerationRecord<T[]>(name, ttlMs)
  return Array.isArray(record?.value) ? record.value : []
}

export function writeGenerationHistory<T>(
  name: string,
  entries: T[],
  limit = GENERATION_HISTORY_LIMIT
): void {
  writeGenerationRecord(name, entries.slice(0, limit))
}

export function removeGenerationRecord(name: string): void {
  if (typeof window === 'undefined') return

  try {
    window.localStorage.removeItem(storageKey(name))
  } catch {
    // Storage may be unavailable; there is nothing else to clean up.
  }
}
