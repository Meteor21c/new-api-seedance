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

import { api } from '@/lib/api'

import {
  GENERATION_PENDING_TTL_MS,
  readGenerationRecord,
  readGenerationHistory,
  removeGenerationRecord,
  writeGenerationHistory,
  writeGenerationRecord,
} from '../generation-storage'
import { saveImageGeneration } from './storage'

export type ImageGenerationRequest = {
  model: string
  /** Optional authorized group override; omitted requests auto-match a usable group. */
  group?: string
  prompt: string
  n: number
  size?: string
  quality?: string
  response_format: 'url'
}

export type GeneratedImage = {
  url?: string
  b64_json?: string
  revised_prompt?: string
}

export type ImageGenerationResponse = {
  created?: number
  data: GeneratedImage[]
}

export type TrackedImageRequest = {
  id: string
  request: ImageGenerationRequest
}

export type ImageGenerationInput = {
  request: ImageGenerationRequest
  referenceImages?: File[]
}

export type TrackedImageResult = {
  id: string
  images: GeneratedImage[]
}

export type ImageHistoryEntry = {
  id: string
  createdAt: number
  model: string
  prompt: string
  images: GeneratedImage[]
}

function persistableImages(images: GeneratedImage[]): GeneratedImage[] {
  return images
    .map(({ url, revised_prompt }) => ({
      ...(url && !url.startsWith('data:') ? { url } : {}),
      ...(revised_prompt ? { revised_prompt } : {}),
    }))
    .filter((image) => image.url || image.revised_prompt)
}

type GenerationModel = {
  id: string
}

type GenerationModelsResponse = {
  success?: boolean
  data?: GenerationModel[]
  fallback?: boolean
}

export async function createImage(
  input: ImageGenerationInput
): Promise<ImageGenerationResponse> {
  const { request, referenceImages = [] } = input
  if (referenceImages.length > 0) {
    const form = new FormData()
    form.append('model', request.model)
    form.append('prompt', request.prompt)
    form.append('n', String(request.n))
    form.append('response_format', request.response_format)
    if (request.group) form.append('group', request.group)
    if (request.size) form.append('size', request.size)
    if (request.quality) form.append('quality', request.quality)
    const fieldName = referenceImages.length === 1 ? 'image' : 'image[]'
    referenceImages.forEach((file) => form.append(fieldName, file, file.name))

    const response = await api.post<ImageGenerationResponse>(
      '/pg/images/edits',
      form,
      { skipErrorHandler: true }
    )
    return response.data
  }

  const response = await api.post<ImageGenerationResponse>(
    '/pg/images/generations',
    request,
    { skipErrorHandler: true }
  )
  return response.data
}

/**
 * Keep a synchronous image request observable after the route is unmounted.
 * Image generation has no server task id, so this is the only way to recover
 * a request when a user switches sections without refreshing the browser.
 */
export async function createImageTracked(
  input: ImageGenerationInput
): Promise<ImageGenerationResponse> {
  const { request } = input
  const requestId = `${Date.now()}-${Math.random().toString(36).slice(2)}`
  writeGenerationRecord<TrackedImageRequest>('image-pending', {
    id: requestId,
    request,
  })
  try {
    const response = await createImage(input)
    const createdAt = Date.now()
    const entry: ImageHistoryEntry = {
      id: requestId,
      createdAt,
      model: request.model,
      prompt: request.prompt,
      images: response.data ?? [],
    }

    // IndexedDB can retain base64 results as browser-local Blobs without
    // consuming application-server storage. A storage quota/private-mode
    // failure must not turn a successful generation into a failed request.
    try {
      await saveImageGeneration(entry)
    } catch {
      // The URL-only localStorage fallback below remains available.
    }

    const persistable = persistableImages(response.data ?? [])
    writeGenerationRecord('image-latest', {
      id: requestId,
      images: persistable,
    } satisfies TrackedImageResult)
    const history = readGenerationHistory<ImageHistoryEntry>('image-history')
    writeGenerationHistory('image-history', [
      { ...entry, images: persistable },
      ...history,
    ])
    return response
  } finally {
    const pending = readGenerationRecord<TrackedImageRequest>(
      'image-pending',
      GENERATION_PENDING_TTL_MS
    )
    if (pending?.value.id === requestId) {
      removeGenerationRecord('image-pending')
    }
  }
}

export async function getImageModels(): Promise<string[]> {
  const response = await api.get<GenerationModelsResponse>(
    '/api/user/generation_models',
    {
      params: { type: 'image' },
      skipErrorHandler: true,
    }
  )
  return (response.data.data ?? []).map((model) => model.id)
}
