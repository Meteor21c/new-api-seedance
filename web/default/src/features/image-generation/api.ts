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

export type ImageGenerationRequest = {
  model: string
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
  request: ImageGenerationRequest
): Promise<ImageGenerationResponse> {
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
export function createImageTracked(
  request: ImageGenerationRequest
): Promise<ImageGenerationResponse> {
  const requestId = `${Date.now()}-${Math.random().toString(36).slice(2)}`
  writeGenerationRecord<TrackedImageRequest>('image-pending', {
    id: requestId,
    request,
  })
  const promise = createImage(request)

  void promise
    .then((response) => {
      writeGenerationRecord('image-latest', {
        id: requestId,
        // Do not put base64 image bytes in localStorage. Only an upstream URL
        // and the descriptive metadata are safe to restore after a reload.
        images: persistableImages(response.data ?? []),
      } satisfies TrackedImageResult)
      const history = readGenerationHistory<ImageHistoryEntry>('image-history')
      writeGenerationHistory('image-history', [
        {
          id: requestId,
          createdAt: Date.now(),
          model: request.model,
          prompt: request.prompt,
          images: persistableImages(response.data ?? []),
        },
        ...history,
      ])
      const pending = readGenerationRecord<TrackedImageRequest>(
        'image-pending',
        GENERATION_PENDING_TTL_MS
      )
      if (pending?.value.id === requestId) {
        removeGenerationRecord('image-pending')
      }
    })
    .catch(() => {
      const pending = readGenerationRecord<TrackedImageRequest>(
        'image-pending',
        GENERATION_PENDING_TTL_MS
      )
      if (pending?.value.id === requestId) {
        removeGenerationRecord('image-pending')
      }
    })

  return promise
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
