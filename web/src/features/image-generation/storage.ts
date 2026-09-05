/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import {
  GENERATION_HISTORY_LIMIT,
  GENERATION_RESULT_TTL_MS,
  getGenerationUserScope,
} from '../generation-storage'
import type { GeneratedImage, ImageHistoryEntry } from './api'

const DATABASE_NAME = 'newapi-image-generation-v1'
const DATABASE_VERSION = 1
const STORE_NAME = 'generations'
const MAX_HISTORY_BYTES = 128 * 1024 * 1024

type StoredImage = {
  url?: string
  blob?: Blob
  revised_prompt?: string
}

type StoredImageHistoryEntry = Omit<ImageHistoryEntry, 'images'> & {
  key: string
  scope: string
  images: StoredImage[]
  sizeBytes: number
}

export type LoadedImageHistory = {
  entries: ImageHistoryEntry[]
  objectUrls: string[]
}

function requestResult<T>(request: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    request.addEventListener('success', () => resolve(request.result), {
      once: true,
    })
    request.addEventListener('error', () => reject(request.error), {
      once: true,
    })
  })
}

function transactionDone(transaction: IDBTransaction): Promise<void> {
  return new Promise((resolve, reject) => {
    transaction.addEventListener('complete', () => resolve(), { once: true })
    transaction.addEventListener('abort', () => reject(transaction.error), {
      once: true,
    })
    transaction.addEventListener('error', () => reject(transaction.error), {
      once: true,
    })
  })
}

function openDatabase(): Promise<IDBDatabase> {
  if (typeof window === 'undefined' || !window.indexedDB) {
    return Promise.reject(new Error('IndexedDB is unavailable'))
  }

  return new Promise((resolve, reject) => {
    const request = window.indexedDB.open(DATABASE_NAME, DATABASE_VERSION)
    request.addEventListener('upgradeneeded', () => {
      const database = request.result
      if (!database.objectStoreNames.contains(STORE_NAME)) {
        const store = database.createObjectStore(STORE_NAME, { keyPath: 'key' })
        store.createIndex('scope', 'scope', { unique: false })
      }
    })
    request.addEventListener('success', () => resolve(request.result), {
      once: true,
    })
    request.addEventListener('error', () => reject(request.error), {
      once: true,
    })
  })
}

function decodeBase64(value: string, mimeType = 'image/png'): Blob {
  const commaIndex = value.indexOf(',')
  const payload = commaIndex >= 0 ? value.slice(commaIndex + 1) : value
  const header = commaIndex >= 0 ? value.slice(0, commaIndex) : ''
  const detectedMime = /^data:([^;]+)/.exec(header)?.[1] || mimeType
  const binary = window.atob(payload)
  const bytes = new Uint8Array(binary.length)
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index)
  }
  return new Blob([bytes], { type: detectedMime })
}

function toStoredImage(image: GeneratedImage): StoredImage | null {
  const revisedPrompt = image.revised_prompt?.trim()
  if (image.b64_json) {
    const blob = decodeBase64(image.b64_json)
    return { blob, ...(revisedPrompt ? { revised_prompt: revisedPrompt } : {}) }
  }
  if (image.url?.startsWith('data:')) {
    const blob = decodeBase64(image.url)
    return { blob, ...(revisedPrompt ? { revised_prompt: revisedPrompt } : {}) }
  }
  if (image.url || revisedPrompt) {
    return {
      ...(image.url ? { url: image.url } : {}),
      ...(revisedPrompt ? { revised_prompt: revisedPrompt } : {}),
    }
  }
  return null
}

async function recordsForScope(
  database: IDBDatabase,
  scope: string
): Promise<StoredImageHistoryEntry[]> {
  const transaction = database.transaction(STORE_NAME, 'readonly')
  const done = transactionDone(transaction)
  const records = await requestResult(
    transaction.objectStore(STORE_NAME).index('scope').getAll(scope)
  )
  await done
  return records as StoredImageHistoryEntry[]
}

async function trimHistory(
  database: IDBDatabase,
  scope: string
): Promise<void> {
  const records = (await recordsForScope(database, scope)).sort(
    (left, right) => right.createdAt - left.createdAt
  )
  const now = Date.now()
  let retainedBytes = 0
  const keysToDelete: string[] = []

  records.forEach((record, index) => {
    const expired = now - record.createdAt > GENERATION_RESULT_TTL_MS
    const overCount = index >= GENERATION_HISTORY_LIMIT
    const nextBytes = retainedBytes + (record.sizeBytes || 0)
    const overBytes = index > 0 && nextBytes > MAX_HISTORY_BYTES
    if (expired || overCount || overBytes) {
      keysToDelete.push(record.key)
      return
    }
    retainedBytes = nextBytes
  })

  if (keysToDelete.length === 0) return
  const transaction = database.transaction(STORE_NAME, 'readwrite')
  const done = transactionDone(transaction)
  const store = transaction.objectStore(STORE_NAME)
  keysToDelete.forEach((key) => store.delete(key))
  await done
}

export async function saveImageGeneration(
  entry: ImageHistoryEntry
): Promise<void> {
  const scope = getGenerationUserScope()
  const images = entry.images
    .map(toStoredImage)
    .filter((image): image is StoredImage => image !== null)
  const sizeBytes = images.reduce(
    (total, image) => total + (image.blob?.size ?? 0),
    0
  )
  const database = await openDatabase()

  try {
    await trimHistory(database, scope)
    const transaction = database.transaction(STORE_NAME, 'readwrite')
    const done = transactionDone(transaction)
    transaction.objectStore(STORE_NAME).put({
      ...entry,
      key: `${scope}:${entry.id}`,
      scope,
      images,
      sizeBytes,
    } satisfies StoredImageHistoryEntry)
    await done
    await trimHistory(database, scope)
  } finally {
    database.close()
  }
}

export async function loadImageGenerationHistory(): Promise<LoadedImageHistory> {
  const scope = getGenerationUserScope()
  const database = await openDatabase()
  try {
    await trimHistory(database, scope)
    const records = (await recordsForScope(database, scope)).sort(
      (left, right) => right.createdAt - left.createdAt
    )
    const objectUrls: string[] = []
    const entries = records.map<ImageHistoryEntry>((record) => ({
      id: record.id,
      createdAt: record.createdAt,
      model: record.model,
      prompt: record.prompt,
      images: record.images.map((image) => {
        const objectUrl = image.blob
          ? window.URL.createObjectURL(image.blob)
          : image.url
        if (image.blob && objectUrl) objectUrls.push(objectUrl)
        return {
          ...(objectUrl ? { url: objectUrl } : {}),
          ...(image.revised_prompt
            ? { revised_prompt: image.revised_prompt }
            : {}),
        }
      }),
    }))
    return { entries, objectUrls }
  } finally {
    database.close()
  }
}

export function revokeImageObjectUrls(urls: string[]): void {
  urls.forEach((url) => window.URL.revokeObjectURL(url))
}
