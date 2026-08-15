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

For commercial licensing, please contact support@quantumnous.com
*/

import { getGenerationUserScope } from '../generation-storage'

const DATABASE_NAME = 'newapi-video-generation-v1'
const DATABASE_VERSION = 1
const STORE_NAME = 'draft-files'

type StoredDraftFile = {
  blob: Blob
  name: string
  type: string
  lastModified: number
}

type StoredVideoDraftFiles = {
  key: string
  referenceFiles: StoredDraftFile[]
  startFrameFile?: StoredDraftFile
  endFrameFile?: StoredDraftFile
}

export type VideoDraftFiles = {
  referenceFiles: File[]
  startFrameFile: File | null
  endFrameFile: File | null
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
        database.createObjectStore(STORE_NAME, { keyPath: 'key' })
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

function storeFile(file: File): StoredDraftFile {
  return {
    blob: file.slice(0, file.size, file.type),
    name: file.name,
    type: file.type,
    lastModified: file.lastModified,
  }
}

function restoreFile(file: StoredDraftFile): File {
  return new File([file.blob], file.name, {
    type: file.type,
    lastModified: file.lastModified,
  })
}

export async function loadVideoDraftFiles(): Promise<VideoDraftFiles> {
  const database = await openDatabase()
  try {
    const transaction = database.transaction(STORE_NAME, 'readonly')
    const done = transactionDone(transaction)
    const record = (await requestResult(
      transaction.objectStore(STORE_NAME).get(getGenerationUserScope())
    )) as StoredVideoDraftFiles | undefined
    await done
    return {
      referenceFiles: (record?.referenceFiles ?? []).map(restoreFile),
      startFrameFile: record?.startFrameFile
        ? restoreFile(record.startFrameFile)
        : null,
      endFrameFile: record?.endFrameFile
        ? restoreFile(record.endFrameFile)
        : null,
    }
  } finally {
    database.close()
  }
}

export async function saveVideoDraftFiles(
  files: VideoDraftFiles
): Promise<void> {
  const database = await openDatabase()
  try {
    const transaction = database.transaction(STORE_NAME, 'readwrite')
    const done = transactionDone(transaction)
    const store = transaction.objectStore(STORE_NAME)
    const key = getGenerationUserScope()
    if (
      files.referenceFiles.length === 0 &&
      !files.startFrameFile &&
      !files.endFrameFile
    ) {
      store.delete(key)
    } else {
      store.put({
        key,
        referenceFiles: files.referenceFiles.map(storeFile),
        ...(files.startFrameFile
          ? { startFrameFile: storeFile(files.startFrameFile) }
          : {}),
        ...(files.endFrameFile
          ? { endFrameFile: storeFile(files.endFrameFile) }
          : {}),
      } satisfies StoredVideoDraftFiles)
    }
    await done
  } finally {
    database.close()
  }
}

export async function clearVideoDraftFiles(): Promise<void> {
  const database = await openDatabase()
  try {
    const transaction = database.transaction(STORE_NAME, 'readwrite')
    const done = transactionDone(transaction)
    transaction.objectStore(STORE_NAME).delete(getGenerationUserScope())
    await done
  } finally {
    database.close()
  }
}
